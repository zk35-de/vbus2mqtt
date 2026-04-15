package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"git.zk35.de/secalpha/vbus2mqtt/internal/config"
	"git.zk35.de/secalpha/vbus2mqtt/internal/vbus"
)

// Payload is the JSON structure published to MQTT.
type Payload struct {
	Device    string             `json:"device"`
	Source    string             `json:"source"`
	Timestamp int64              `json:"timestamp"`
	Fields    map[string]float64 `json:"fields"`
	Units     map[string]string  `json:"units,omitempty"`
}

// Client wraps paho.mqtt with structured logging and reconnect handling.
// paho handles reconnect automatically; this wrapper adds logging hooks.
type Client struct {
	cfg    *config.Config
	inner  paho.Client
	log    *slog.Logger
}

func New(cfg *config.Config, log *slog.Logger) *Client {
	c := &Client{cfg: cfg, log: log}

	opts := paho.NewClientOptions().
		AddBroker(cfg.MQTTBroker).
		SetClientID("vbus2mqtt").
		SetAutoReconnect(true).
		SetMaxReconnectInterval(30 * time.Second).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetOnConnectHandler(func(_ paho.Client) {
			log.Info("mqtt connected", "broker", cfg.MQTTBroker)
		}).
		SetConnectionLostHandler(func(_ paho.Client, err error) {
			log.Warn("mqtt connection lost, reconnecting", "err", err)
		}).
		SetReconnectingHandler(func(_ paho.Client, _ *paho.ClientOptions) {
			log.Info("mqtt reconnecting…")
		})

	if cfg.MQTTUser != "" {
		opts.SetUsername(cfg.MQTTUser)
		opts.SetPassword(cfg.MQTTPass)
	}

	c.inner = paho.NewClient(opts)
	return c
}

// Connect performs the initial broker connection.
// Returns nil even if the broker is unreachable – paho will keep retrying.
func (c *Client) Connect(_ context.Context) error {
	token := c.inner.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		c.log.Warn("mqtt initial connect timed out, continuing in background")
		return nil
	}
	if err := token.Error(); err != nil {
		c.log.Warn("mqtt initial connect failed, will retry", "err", err)
	}
	return nil
}

// Publish serialises telemetry and sends it to
// <prefix>/<SRC_HEX_ADDR> (e.g. vbus/7112).
func (c *Client) Publish(src uint16, device string, fields []vbus.TelemetryField) error {
	if !c.inner.IsConnected() {
		c.log.Debug("mqtt not connected, skipping publish")
		return nil
	}

	p := Payload{
		Device:    device,
		Source:    fmt.Sprintf("0x%04X", src),
		Timestamp: time.Now().Unix(),
		Fields:    make(map[string]float64, len(fields)),
		Units:     make(map[string]string, len(fields)),
	}
	for _, f := range fields {
		p.Fields[f.Name] = f.Value
		if f.Unit != "" {
			p.Units[f.Name] = f.Unit
		}
	}
	if len(p.Units) == 0 {
		p.Units = nil
	}

	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	topic := fmt.Sprintf("%s/%04X", c.cfg.MQTTTopicPrefix, src)
	token := c.inner.Publish(topic, c.cfg.MQTTQOS, c.cfg.MQTTRetain, data)
	if !token.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("mqtt publish timeout on topic %s", topic)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt publish: %w", err)
	}

	c.log.Debug("mqtt published",
		"topic", topic,
		"device", device,
		"fields", len(fields),
	)
	return nil
}

func (c *Client) Disconnect() {
	c.inner.Disconnect(2000)
}
