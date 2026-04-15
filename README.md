# vbus2mqtt

Liest den [Resol VBus](https://danielwippermann.github.io/resol-vbus/) Datenstrom
vom USB-Seriell-Adapter und publiziert die dekodierten Sensorwerte als MQTT-Topics.

## Topics

```
vbus/<source_addr>/<field>
vbus/<source_addr>/last_update   # Unix-Timestamp
```

Beispiel (DeltaSol BS Plus, Quell-Adresse 0x7110):

```
vbus/7110/temp_sensor1    → 67.3
vbus/7110/temp_sensor2    → 22.1
vbus/7110/pump_speed1     → 100
vbus/7110/heat_quantity   → 12345
```

## Unterstützte Controller

| Hersteller | Gerät           | Src    | Dst    | Cmd    |
|-----------|----------------|--------|--------|--------|
| Resol     | DeltaSol BS+   | 0x7110 | 0x0010 | 0x0100 |

Weitere Controller können in `FIELD_DEFS` in `vbus2mqtt.py` ergänzt werden.
Rohframes (unbekannte Geräte) erscheinen als DEBUG-Log.

## Quickstart

```bash
cp .env.example .env
$EDITOR .env          # MQTT_HOST + SERIAL_PORT anpassen

# Podman
podman-compose up -d

# Oder direkt
podman build -t vbus2mqtt -f Containerfile .
podman run -d \
  --name vbus2mqtt \
  --device /dev/ttyUSB0:/dev/ttyUSB0 \
  --network host \
  --env-file .env \
  --restart unless-stopped \
  vbus2mqtt
```

## Serial device Berechtigungen

```bash
# Host-User in dialout-Gruppe (einmalig, Relogin nötig):
sudo usermod -aG dialout $USER

# Gerät prüfen:
ls -la /dev/ttyUSB*
```

## Logs

```bash
podman logs -f vbus2mqtt
```

## Controller-Konfiguration erweitern

Neue Feldliste in `vbus2mqtt.py` unter `FIELD_DEFS` ergänzen:

```python
(0x0010, 0xXXXX, 0x0100): [
    (offset, length_bytes, scale_divisor, "name", "unit"),
    ...
],
```

Rohframes mit `LOG_LEVEL=DEBUG` beobachten um Offsets zu ermitteln.
Referenz: [VBus specification](https://danielwippermann.github.io/resol-vbus/vbus-specification.html)
