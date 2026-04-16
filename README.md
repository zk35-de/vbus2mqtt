# vbus2mqtt

RESOL VBus (USB serial adapter) → MQTT bridge with web UI.

Reads the VBus data stream from a solar controller, publishes JSON telemetry to MQTT,
and exposes a settings web UI at `:8080` for runtime configuration without restarts.

Written in Go. Runs as a rootless Docker/Podman container. Multi-arch (amd64 + arm64).

---

## Web UI

Open `http://<host>:8080` after startup.

- Live status: MQTT connection, last publish, detected devices, uptime
- Edit all settings at runtime — changes take effect immediately
- Settings are persisted to `/data/config.json` and survive container restarts
- MQTT broker / credentials / serial port changes trigger an automatic reconnect
- Log level changes take effect within one second (no reconnect needed)

---

## MQTT output

Topic: `<MQTT_TOPIC_PREFIX>/<SOURCE_ADDR_HEX>`

Example for a DeltaSol BS (source address `0x7112`):

```
Topic:   vbus/7112
Payload: {
  "device":    "DeltaSol BS",
  "source":    "0x7112",
  "timestamp": 1713180000,
  "fields": {
    "temp_sensor1":    67.3,
    "temp_sensor2":    22.1,
    "pump_speed":      100,
    "operating_hours": 1234,
    "error_mask":      0
  },
  "units": {
    "temp_sensor1":    "°C",
    "temp_sensor2":    "°C",
    "pump_speed":      "%",
    "operating_hours": "h"
  }
}
```

## Supported controllers

| Device             | Source   | Destination | Command  |
|--------------------|----------|-------------|----------|
| DeltaSol BS        | `0x7112` | `0x0010`    | `0x0100` |
| DeltaSol BS Plus   | `0x7110` | `0x0010`    | `0x0100` |
| DeltaSol C         | `0x7111` | `0x0010`    | `0x0100` |

Unknown devices are logged at DEBUG level with their raw payload hex.
Add new devices in `internal/vbus/registry.go`.

---

## Quickstart

```bash
git clone https://github.com/zk35-de/vbus2mqtt
cd vbus2mqtt

cp .env.example .env
$EDITOR .env        # set MQTT_BROKER at minimum

docker compose up -d vbus2mqtt   # omit mosquitto if you have a broker
docker logs -f vbus2mqtt

# Open http://localhost:8080 for the settings UI
```

## Build

### Local (requires Go 1.23+)

```bash
go build -ldflags "-X main.version=$(git describe --tags --always)" \
  -o vbus2mqtt ./cmd/vbus2mqtt
```

### Docker single-arch

```bash
docker build --build-arg VERSION=$(git describe --tags --always) -t vbus2mqtt .
```

### Multi-arch with buildx

```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --build-arg VERSION=$(git describe --tags --always) \
  -t registry.example.com/vbus2mqtt:latest \
  --push .
```

---

## Configuration

All settings can be changed at runtime via the web UI at `:8080`.
Environment variables set the initial defaults.

| Variable           | Default                  | Description                              |
|--------------------|--------------------------|------------------------------------------|
| `SERIAL_PORT`      | *(auto-detect)*          | e.g. `/dev/ttyUSB0`                      |
| `SERIAL_BAUD`      | `9600`                   | VBus baud rate (always 9600)             |
| `MQTT_BROKER`      | `tcp://localhost:1883`   | MQTT broker URL                          |
| `MQTT_TOPIC_PREFIX`| `vbus`                   | Topic prefix                             |
| `MQTT_USER`        |                          | MQTT username (optional)                 |
| `MQTT_PASS`        |                          | MQTT password (optional)                 |
| `MQTT_QOS`         | `0`                      | MQTT QoS level (0/1/2)                   |
| `MQTT_RETAIN`      | `true`                   | Retain last message on broker            |
| `PUBLISH_INTERVAL` | `30s`                    | How often to push telemetry              |
| `LOG_LEVEL`        | `info`                   | `debug` \| `info` \| `warn` \| `error`  |
| `LOG_FORMAT`       | `json`                   | `json` \| `text`                         |
| `WEB_ADDR`         | `:8080`                  | HTTP listen address for the web UI       |
| `CONFIG_FILE`      | `/data/config.json`      | Persistence path for web UI changes      |

### Config persistence

Settings saved via the web UI are written atomically to `CONFIG_FILE`.
Mount `/data` as a Docker volume so changes survive container restarts:

```yaml
volumes:
  - vbus2mqtt_data:/data
```

The file-based config takes precedence over environment variables.
To reset to env-var defaults, delete the file and restart.

---

## HTTP API

| Method | Path         | Description                          |
|--------|--------------|--------------------------------------|
| `GET`  | `/`          | Settings web UI                      |
| `GET`  | `/health`    | `{"status":"ok","version":"..."}` |
| `GET`  | `/api/config`| Current config (password redacted)   |
| `PUT`  | `/api/config`| Update config (JSON body)            |
| `GET`  | `/api/status`| Runtime status (MQTT, devices, etc.) |

---

## USB / serial access on Linux

```bash
# Check device node:
ls -la /dev/ttyUSB* /dev/ttyACM*

# Add your user to the dialout group (once, then re-login):
sudo usermod -aG dialout $USER

# Verify with minicom:
minicom -D /dev/ttyUSB0 -b 9600
# You should see garbled binary data from the VBus stream.
```

On **Raspberry Pi** the adapter is usually `/dev/ttyUSB0`.
If you use a USB hub, the path may change after reboot – add a udev rule:

```
# /etc/udev/rules.d/99-vbus.rules
SUBSYSTEM=="tty", ATTRS{idVendor}=="10c4", ATTRS{idProduct}=="ea60", SYMLINK+="ttyVBUS"
```

Then set `SERIAL_PORT=/dev/ttyVBUS`.

---

## Troubleshooting

**Device not detected**
```
docker logs vbus2mqtt | grep "serial"
# → try setting SERIAL_PORT explicitly via the web UI or .env
```

**Unknown device / no telemetry**
```bash
# Enable debug logging via web UI, or:
LOG_LEVEL=debug docker compose up vbus2mqtt
# Look for: "unknown vbus device" src=0xXXXX payload_hex=...
# Add the source address + field offsets to internal/vbus/registry.go
```

**Permission denied on /dev/ttyUSB0**
```bash
sudo chmod a+rw /dev/ttyUSB0    # temporary
# or: add user to dialout group (permanent)
```

**Wrong temperatures (e.g. 6553.5°C)**
The source address doesn't match any registry entry, so a different device's
fields are decoded. Run with `LOG_LEVEL=debug` and check the actual source
address in the log output.
