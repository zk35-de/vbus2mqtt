package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"git.zk35.de/secalpha/vbus2mqtt/internal/config"
	"git.zk35.de/secalpha/vbus2mqtt/internal/device"
	"git.zk35.de/secalpha/vbus2mqtt/internal/mqtt"
	"git.zk35.de/secalpha/vbus2mqtt/internal/vbus"
)

func main() {
	cfg := config.Load()
	log := newLogger(cfg.LogLevel, cfg.LogFormat)

	log.Info("vbus2mqtt starting",
		"broker", cfg.MQTTBroker,
		"topic_prefix", cfg.MQTTTopicPrefix,
		"publish_interval", cfg.PublishInterval,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dev, err := device.Open(cfg, log)
	if err != nil {
		log.Error("failed to open serial device", "err", err)
		os.Exit(1)
	}
	defer dev.Close()

	mqttClient := mqtt.New(cfg, log)
	if err := mqttClient.Connect(ctx); err != nil {
		log.Error("mqtt connect failed", "err", err)
		os.Exit(1)
	}
	defer mqttClient.Disconnect()

	parser := vbus.NewParser(log)
	buf := make([]byte, 512)

	type fieldSet struct {
		device string
		fields []vbus.TelemetryField
	}
	pending := make(map[uint16]*fieldSet)
	ticker := time.NewTicker(cfg.PublishInterval)
	defer ticker.Stop()

	log.Info("reading VBus data", "port", dev.Path())

	for {
		select {
		case <-ctx.Done():
			log.Info("shutdown signal received")
			return
		case <-ticker.C:
			for src, fs := range pending {
				if err := mqttClient.Publish(src, fs.device, fs.fields); err != nil {
					log.Warn("mqtt publish failed", "src", fmt.Sprintf("0x%04X", src), "err", err)
				}
			}
			pending = make(map[uint16]*fieldSet)
		default:
			n, err := dev.Read(buf)
			if err != nil {
				continue
			}
			if n == 0 {
				continue
			}

			frames := parser.Feed(buf[:n])
			for _, f := range frames {
				devName, fields, known := vbus.Decode(f, vbus.DefaultRegistry)
				if !known {
					log.Debug("unknown vbus device",
						"src", fmt.Sprintf("0x%04X", f.Source),
						"dst", fmt.Sprintf("0x%04X", f.Destination),
						"cmd", fmt.Sprintf("0x%04X", f.Command),
						"payload_hex", hex.EncodeToString(f.Payload),
					)
					continue
				}
				pending[f.Source] = &fieldSet{device: devName, fields: fields}
			}
		}
	}
}

func newLogger(level, format string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler
	if format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}
