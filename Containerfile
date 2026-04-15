FROM python:3.13-slim

LABEL org.opencontainers.image.title="vbus2mqtt" \
      org.opencontainers.image.description="Resol VBus (USB serial) → MQTT bridge" \
      org.opencontainers.image.source="https://git.zk35.de/secalpha/vbus2mqtt"

# Non-root user (rootless Podman)
RUN groupadd -g 1000 vbus && useradd -u 1000 -g vbus -s /sbin/nologin -M vbus

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY vbus2mqtt.py .

# dialout group (999) for serial device access – adjust GID if needed on host
# Podman: pass device with --device /dev/ttyUSB0:/dev/ttyUSB0
USER vbus

CMD ["python", "-u", "vbus2mqtt.py"]
