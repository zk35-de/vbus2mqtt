#!/usr/bin/env python3
"""
vbus2mqtt – Resol VBus (USB serial) → MQTT bridge

Liest den VBus-Datenstrom vom seriellen Port und publiziert
dekodierte Feldwerte als MQTT-Topics.

Konfiguration via Umgebungsvariablen (siehe README / .env.example).
"""

import asyncio
import logging
import os
import signal
import struct
import sys
import time
from dataclasses import dataclass, field
from typing import Optional

import paho.mqtt.client as mqtt
import serial

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------
LOG_LEVEL = os.getenv("LOG_LEVEL", "INFO").upper()
logging.basicConfig(
    level=getattr(logging, LOG_LEVEL, logging.INFO),
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
    datefmt="%Y-%m-%dT%H:%M:%S",
    stream=sys.stdout,
)
log = logging.getLogger("vbus2mqtt")

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------
SERIAL_PORT  = os.getenv("SERIAL_PORT", "/dev/ttyUSB0")
SERIAL_BAUD  = int(os.getenv("SERIAL_BAUD", "9600"))

MQTT_HOST    = os.getenv("MQTT_HOST", "localhost")
MQTT_PORT    = int(os.getenv("MQTT_PORT", "1883"))
MQTT_USER    = os.getenv("MQTT_USER", "")
MQTT_PASS    = os.getenv("MQTT_PASS", "")
MQTT_PREFIX  = os.getenv("MQTT_PREFIX", "vbus")
MQTT_QOS     = int(os.getenv("MQTT_QOS", "0"))
MQTT_RETAIN  = os.getenv("MQTT_RETAIN", "true").lower() == "true"

PUBLISH_INTERVAL = float(os.getenv("PUBLISH_INTERVAL", "30"))  # seconds

# ---------------------------------------------------------------------------
# VBus Protocol v1 parser
# Spec: https://danielwippermann.github.io/resol-vbus/vbus-specification.html
# ---------------------------------------------------------------------------
VBUS_SYNC = 0xAA

@dataclass
class VBusFrame:
    destination: int
    source: int
    protocol_version: int
    command: int
    payload: bytes = field(default_factory=bytes)


def _check_lsb(data: bytes) -> bool:
    """Verify that no byte in data has bit 7 set (VBus data byte constraint)."""
    return all(b & 0x80 == 0 for b in data)


def _inject_septett(data: bytearray, septett: int) -> None:
    """Re-inject bit 7 into up to 4 payload bytes using the septett byte."""
    for i in range(min(4, len(data))):
        if septett & (1 << i):
            data[i] |= 0x80


def _calc_checksum(data: bytes) -> int:
    total = 0x7F
    for b in data:
        total = (total - b) & 0x7F
    return total


class VBusParser:
    """
    Minimal VBus protocol v1 parser.

    Only handles protocol version 0x10 (standard data telegrams).
    Extend for v2/v3 if needed.
    """

    def __init__(self) -> None:
        self._buf: bytearray = bytearray()
        self.frames: list[VBusFrame] = []

    def feed(self, data: bytes) -> None:
        self._buf.extend(data)
        self._parse()

    def _parse(self) -> None:
        buf = self._buf
        while len(buf) >= 6:
            # Find SYNC byte
            if buf[0] != VBUS_SYNC:
                idx = buf.find(VBUS_SYNC)
                if idx < 0:
                    buf.clear()
                    break
                del buf[:idx]
                continue

            # Minimum header: SYNC(1) + DST(2) + SRC(2) + VER(1) + CMD(2) + framecnt(1) + CS(1) = 10
            if len(buf) < 10:
                break

            dst    = buf[1] | (buf[2] << 8)
            src    = buf[3] | (buf[4] << 8)
            ver    = buf[5]
            cmd    = buf[6] | (buf[7] << 8)
            nframe = buf[8]
            cs     = buf[9]

            if ver != 0x10:
                # Not v1 – skip this SYNC byte and continue
                del buf[0]
                continue

            # Verify header checksum (bytes 1..8)
            if _calc_checksum(bytes(buf[1:9])) != cs:
                log.debug("Header checksum mismatch, skipping SYNC")
                del buf[0]
                continue

            # Each data frame is 6 bytes: 4 data + 1 septett + 1 checksum
            total_len = 10 + nframe * 6
            if len(buf) < total_len:
                break  # wait for more data

            payload = bytearray()
            ok = True
            pos = 10
            for _ in range(nframe):
                chunk   = buf[pos:pos + 4]
                septett = buf[pos + 4]
                frame_cs = buf[pos + 5]
                if _calc_checksum(bytes(buf[pos:pos + 5])) != frame_cs:
                    log.debug("Frame checksum mismatch")
                    ok = False
                    break
                decoded = bytearray(chunk)
                _inject_septett(decoded, septett)
                payload.extend(decoded)
                pos += 6

            if ok:
                self.frames.append(VBusFrame(
                    destination=dst,
                    source=src,
                    protocol_version=ver,
                    command=cmd,
                    payload=bytes(payload),
                ))

            del buf[:total_len]

        self._buf = buf

    def pop_frames(self) -> list[VBusFrame]:
        frames = self.frames[:]
        self.frames.clear()
        return frames


# ---------------------------------------------------------------------------
# Field definitions – DeltaSol BS Plus (0x7110 → 0x0010, cmd 0x0100)
# Extend this dict for other controllers/commands.
# Format: offset, length (bytes), scale_divisor, name, unit
# ---------------------------------------------------------------------------
FIELD_DEFS: dict[tuple[int, int, int], list[tuple[int, int, float, str, str]]] = {
    # (destination, source, command)
    (0x0010, 0x7110, 0x0100): [
        (0,  2,  10.0, "temp_sensor1",  "°C"),
        (2,  2,  10.0, "temp_sensor2",  "°C"),
        (4,  2,  10.0, "temp_sensor3",  "°C"),
        (6,  2,  10.0, "temp_sensor4",  "°C"),
        (8,  1,   1.0, "pump_speed1",   "%"),
        (9,  1,   1.0, "pump_speed2",   "%"),
        (10, 2,   1.0, "operating_status", ""),
        (12, 4,   1.0, "heat_quantity",  "Wh"),
    ],
}

def decode_fields(frame: VBusFrame) -> dict[str, float | int]:
    """Decode known fields from a VBus frame payload."""
    key = (frame.destination, frame.source, frame.command)
    defs = FIELD_DEFS.get(key)
    if not defs:
        return {}

    result: dict[str, float | int] = {}
    for (offset, length, divisor, name, _unit) in defs:
        if offset + length > len(frame.payload):
            continue
        chunk = frame.payload[offset:offset + length]
        if length == 1:
            raw = chunk[0]
        elif length == 2:
            raw = struct.unpack_from("<h", chunk)[0]  # signed 16-bit LE
        elif length == 4:
            raw = struct.unpack_from("<i", chunk)[0]  # signed 32-bit LE
        else:
            continue
        result[name] = round(raw / divisor, 2) if divisor != 1.0 else raw

    return result


# ---------------------------------------------------------------------------
# MQTT helper
# ---------------------------------------------------------------------------
class MQTTPublisher:
    def __init__(self) -> None:
        self._client = mqtt.Client(client_id="vbus2mqtt", clean_session=True)
        if MQTT_USER:
            self._client.username_pw_set(MQTT_USER, MQTT_PASS)
        self._client.on_connect    = self._on_connect
        self._client.on_disconnect = self._on_disconnect
        self._connected = False

    def _on_connect(self, _client, _userdata, _flags, rc: int) -> None:
        if rc == 0:
            self._connected = True
            log.info("MQTT connected to %s:%d", MQTT_HOST, MQTT_PORT)
        else:
            log.error("MQTT connect failed, rc=%d", rc)

    def _on_disconnect(self, _client, _userdata, rc: int) -> None:
        self._connected = False
        log.warning("MQTT disconnected (rc=%d)", rc)

    def connect(self) -> None:
        self._client.connect(MQTT_HOST, MQTT_PORT, keepalive=60)
        self._client.loop_start()

    def publish(self, subtopic: str, value: object) -> None:
        if not self._connected:
            log.debug("MQTT not connected, skipping publish")
            return
        topic = f"{MQTT_PREFIX}/{subtopic}"
        self._client.publish(topic, str(value), qos=MQTT_QOS, retain=MQTT_RETAIN)
        log.debug("→ %s = %s", topic, value)

    def disconnect(self) -> None:
        self._client.loop_stop()
        self._client.disconnect()


# ---------------------------------------------------------------------------
# Main loop
# ---------------------------------------------------------------------------
def run() -> None:
    log.info("vbus2mqtt starting – port=%s baud=%d mqtt=%s:%d prefix=%s",
             SERIAL_PORT, SERIAL_BAUD, MQTT_HOST, MQTT_PORT, MQTT_PREFIX)

    mqtt_pub = MQTTPublisher()
    mqtt_pub.connect()

    parser = VBusParser()
    accumulated: dict[str, float | int] = {}
    last_publish = 0.0

    # Graceful shutdown
    shutdown = False
    def _handle_signal(_sig, _frame):
        nonlocal shutdown
        log.info("Shutdown requested")
        shutdown = True

    signal.signal(signal.SIGTERM, _handle_signal)
    signal.signal(signal.SIGINT, _handle_signal)

    ser: Optional[serial.Serial] = None

    while not shutdown:
        try:
            if ser is None or not ser.is_open:
                log.info("Opening serial port %s", SERIAL_PORT)
                ser = serial.Serial(
                    SERIAL_PORT,
                    baudrate=SERIAL_BAUD,
                    bytesize=serial.EIGHTBITS,
                    parity=serial.PARITY_NONE,
                    stopbits=serial.STOPBITS_ONE,
                    timeout=1.0,
                )

            data = ser.read(256)
            if data:
                parser.feed(data)
                for frame in parser.pop_frames():
                    fields = decode_fields(frame)
                    if fields:
                        log.debug("Frame src=0x%04X dst=0x%04X cmd=0x%04X → %s",
                                  frame.source, frame.destination, frame.command, fields)
                        accumulated.update(fields)

            now = time.monotonic()
            if accumulated and (now - last_publish) >= PUBLISH_INTERVAL:
                src_hex = f"{frame.source:04X}" if 'frame' in dir() else "unknown"
                for name, value in accumulated.items():
                    mqtt_pub.publish(f"{src_hex}/{name}", value)
                mqtt_pub.publish(f"{src_hex}/last_update", int(time.time()))
                last_publish = now

        except serial.SerialException as exc:
            log.error("Serial error: %s – retrying in 5s", exc)
            if ser:
                ser.close()
                ser = None
            time.sleep(5)
        except Exception as exc:
            log.exception("Unexpected error: %s", exc)
            time.sleep(1)

    if ser and ser.is_open:
        ser.close()
    mqtt_pub.disconnect()
    log.info("vbus2mqtt stopped")


if __name__ == "__main__":
    run()
