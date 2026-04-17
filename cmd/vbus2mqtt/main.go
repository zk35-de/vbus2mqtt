package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	goserial "go.bug.st/serial"

	"git.zk35.de/secalpha/vbus2mqtt/internal/config"
	"git.zk35.de/secalpha/vbus2mqtt/internal/device"
	mqttclient "git.zk35.de/secalpha/vbus2mqtt/internal/mqtt"
	"git.zk35.de/secalpha/vbus2mqtt/internal/status"
	"git.zk35.de/secalpha/vbus2mqtt/internal/vbus"
	"git.zk35.de/secalpha/vbus2mqtt/internal/web"
)

// version is set at build time via -ldflags "-X main.version=<tag>".
var version = "dev"

// errConfigChanged is a sentinel returned by readLoop when the config store
// version changes, signalling the outer loop to reconnect with fresh settings.
var errConfigChanged = errors.New("config changed")

func main() {
	store := config.NewStore(config.Load().ConfigFile)
	cfg := store.Get()

	var logLevel slog.LevelVar
	log := buildLogger(store, &logLevel)
	slog.SetDefault(log)

	log.Info("vbus2mqtt starting",
		"version", version,
		"serial_port", cfg.SerialPort,
		"baud", cfg.BaudRate,
		"mqtt_broker", cfg.MQTTBroker,
		"mqtt_prefix", cfg.MQTTTopicPrefix,
		"publish_interval", cfg.PublishInterval.String(),
		"web_addr", cfg.WebAddr,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	st := status.New()

	// ── Web UI ────────────────────────────────────────────────────────────────
	webSrv := web.New(store, st, version, log)
	go func() {
		if err := webSrv.Start(ctx, cfg.WebAddr); err != nil {
			log.Error("web server error", "err", err)
		}
	}()

	// ── Live log-level updates ────────────────────────────────────────────────
	go watchLogLevel(ctx, store, &logLevel)

	// ── Main bridge loop ──────────────────────────────────────────────────────
	for ctx.Err() == nil {
		ver := store.Version()
		cfg = store.Get()

		mqtt := mqttclient.New(store, log)
		if err := mqtt.Connect(ctx); err != nil {
			log.Error("mqtt connect error", "err", err)
		}
		st.SetMQTTConnected(mqtt.IsConnected())

		// Serial device sub-loop: runs until config changes or context is done.
		for ctx.Err() == nil && store.Version() == ver {
			dev, err := device.Open(&cfg, log)
			if err != nil {
				log.Error("cannot open serial device", "err", err)
				waitOrDone(ctx, 5*time.Second)
				continue
			}

			log.Info("starting read loop", "port", dev.Path())
			loopErr := readLoop(ctx, dev, store, ver, mqtt, st, log)
			dev.Close()

			if loopErr == errConfigChanged {
				break // outer loop will reconnect with new config
			}
			if loopErr != nil {
				log.Error("read loop ended with error", "err", loopErr)
			}

			if ctx.Err() == nil && store.Version() == ver {
				log.Info("reconnecting serial in 3s…")
				waitOrDone(ctx, 3*time.Second)
			}
		}

		st.SetMQTTConnected(false)
		mqtt.Disconnect()

		if ctx.Err() == nil && store.Version() != ver {
			log.Info("config changed, reconnecting…")
		}
	}

	log.Info("vbus2mqtt stopped")
}

// ─── Read loop ────────────────────────────────────────────────────────────────

// accumulator holds the latest decoded telemetry per source address.
type accumulator struct {
	device string
	fields []vbus.TelemetryField
}

func readLoop(
	ctx context.Context,
	dev *device.Device,
	store *config.Store,
	startVer uint64,
	mqtt *mqttclient.Client,
	st *status.Status,
	log *slog.Logger,
) error {
	parser := vbus.NewParser(log)
	accum := make(map[uint16]*accumulator)
	lastPublish := time.Now()
	buf := make([]byte, 512)

	for ctx.Err() == nil {
		// Config changed → signal the outer loop to reconnect.
		if store.Version() != startVer {
			return errConfigChanged
		}

		n, err := dev.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) || isPortError(err) {
				return fmt.Errorf("serial read: %w", err)
			}
			return err
		}
		// n==0 with err==nil is a read-timeout (no data in window) – normal.

		if n > 0 {
			frames := parser.Feed(buf[:n])
			for _, f := range frames {
				deviceName, fields, known := vbus.Decode(f, vbus.DefaultRegistry)
				if !known {
					log.Debug("unknown vbus device – add to registry or file an issue",
						"src", fmt.Sprintf("0x%04X", f.Source),
						"dst", fmt.Sprintf("0x%04X", f.Destination),
						"cmd", fmt.Sprintf("0x%04X", f.Command),
						"payload_hex", fmt.Sprintf("%X", f.Payload),
						"payload_len", len(f.Payload),
					)
					continue
				}
				accum[f.Source] = &accumulator{device: deviceName, fields: fields}
				log.Debug("telemetry updated",
					"device", deviceName,
					"src", fmt.Sprintf("0x%04X", f.Source),
					"fields", len(fields),
				)
			}
		}

		cfg := store.Get()
		if time.Since(lastPublish) >= cfg.PublishInterval && len(accum) > 0 {
			deviceNames := make([]string, 0, len(accum))
			for src, a := range accum {
				if err := mqtt.Publish(src, a.device, a.fields); err != nil {
					log.Error("mqtt publish failed", "src", fmt.Sprintf("0x%04X", src), "err", err)
				}
				deviceNames = append(deviceNames, a.device)
			}
			now := time.Now()
			st.SetLastPublish(now)
			st.SetDevices(deviceNames)
			st.SetMQTTConnected(mqtt.IsConnected())
			lastPublish = now
		}
	}

	return nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func buildLogger(store *config.Store, lv *slog.LevelVar) *slog.Logger {
	setLogLevel(lv, store.Get().LogLevel)
	return slog.New(newDynamicHandler(store, lv))
}

func setLogLevel(lv *slog.LevelVar, level string) {
	switch level {
	case "debug":
		lv.Set(slog.LevelDebug)
	case "warn":
		lv.Set(slog.LevelWarn)
	case "error":
		lv.Set(slog.LevelError)
	default:
		lv.Set(slog.LevelInfo)
	}
}

// watchLogLevel polls the store version and applies log level changes
// immediately without requiring a reconnect.
func watchLogLevel(ctx context.Context, store *config.Store, lv *slog.LevelVar) {
	var lastVer uint64
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if v := store.Version(); v != lastVer {
				lastVer = v
				setLogLevel(lv, store.Get().LogLevel)
			}
		}
	}
}

func waitOrDone(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// isPortError returns true for any goserial.PortError (device disconnect, etc.).
func isPortError(err error) bool {
	var pe *goserial.PortError
	return errors.As(err, &pe)
}
