package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/zk35-de/vbus2mqtt/internal/config"
	"github.com/zk35-de/vbus2mqtt/internal/vbus"
)

// Payload is the JSON structure published to MQTT.
type Payload struct {
	Device    string             `json:"device"`
	Source    string             `json:"source"`
	Timestamp int64              `json:"timestamp"`
	Fields    map[string]float64 `json:"fields"`
	Units     map[string]string  `json:"units,omitempty"`
}

// haDiscoveryPayload is the HA MQTT Autodiscovery config payload.
type haDiscoveryPayload struct {
	Name              string   `json:"name"`
	StateTopic        string   `json:"state_topic"`
	ValueTemplate     string   `json:"value_template"`
	UniqueID          string   `json:"unique_id"`
	UnitOfMeasurement string   `json:"unit_of_measurement,omitempty"`
	DeviceClass       string   `json:"device_class,omitempty"`
	StateClass        string   `json:"state_class,omitempty"`
	Device            haDevice `json:"device"`
}

type haDevice struct {
	Identifiers []string `json:"identifiers"`
	Name        string   `json:"name"`
}

// Client wraps paho.mqtt with structured logging and reconnect handling.
// Connection parameters are fixed at construction time; publish parameters
// (topic prefix, QoS, retain) are read live from the store on each publish.
type Client struct {
	store         *config.Store
	inner         paho.Client
	log           *slog.Logger
	discoveredMu  sync.Mutex
	discovered    map[string]struct{} // key: "<src_hex>/<field>"
}

func New(store *config.Store, log *slog.Logger) *Client {
	cfg := store.Get()
	c := &Client{
		store:      store,
		log:        log,
		discovered: make(map[string]struct{}),
	}

	opts := paho.NewClientOptions().
		AddBroker(cfg.MQTTBroker).
		SetClientID("vbus2mqtt").
		SetAutoReconnect(true).
		SetMaxReconnectInterval(30 * time.Second).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetOnConnectHandler(func(_ paho.Client) {
			log.Info("mqtt connected", "broker", cfg.MQTTBroker)
			// Clear discovery cache so HA gets fresh configs after reconnect.
			c.discoveredMu.Lock()
			c.discovered = make(map[string]struct{})
			c.discoveredMu.Unlock()
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

// IsConnected reports whether the client has an active broker connection.
func (c *Client) IsConnected() bool {
	return c.inner.IsConnected()
}

// Publish serialises telemetry and sends it to
// <prefix>/<SRC_HEX_ADDR> (e.g. vbus/7112).
// Topic prefix, QoS, and retain are read live from the store.
// When HA autodiscovery is enabled, discovery configs are published first
// for any field not yet announced in this connection.
func (c *Client) Publish(src uint16, device string, fields []vbus.TelemetryField) error {
	if !c.inner.IsConnected() {
		c.log.Debug("mqtt not connected, skipping publish")
		return nil
	}

	cfg := c.store.Get()

	if cfg.MQTTHADiscovery {
		c.publishDiscovery(src, device, fields, cfg)
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

	topic := fmt.Sprintf("%s/%04X", cfg.MQTTTopicPrefix, src)
	token := c.inner.Publish(topic, cfg.MQTTQOS, cfg.MQTTRetain, data)
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
		"values", p.Fields,
	)
	return nil
}

// publishDiscovery sends HA MQTT Autodiscovery config payloads for any
// (src, field) pair not yet announced in this connection lifetime.
func (c *Client) publishDiscovery(src uint16, deviceName string, fields []vbus.TelemetryField, cfg config.Config) {
	srcHex := fmt.Sprintf("%04X", src)
	stateTopic := fmt.Sprintf("%s/%s", cfg.MQTTTopicPrefix, srcHex)
	deviceID := fmt.Sprintf("vbus2mqtt_%s", strings.ToLower(srcHex))

	for _, f := range fields {
		cacheKey := srcHex + "/" + f.Name
		c.discoveredMu.Lock()
		_, already := c.discovered[cacheKey]
		if !already {
			c.discovered[cacheKey] = struct{}{}
		}
		c.discoveredMu.Unlock()
		if already {
			continue
		}

		dc, sc := haClassFromUnit(f.Unit)
		slug := fieldSlug(f.Name)
		uniqueID := fmt.Sprintf("vbus2mqtt_%s_%s", strings.ToLower(srcHex), slug)

		payload := haDiscoveryPayload{
			Name:              fmt.Sprintf("%s %s", deviceName, f.Name),
			StateTopic:        stateTopic,
			ValueTemplate:     fmt.Sprintf("{{ value_json.fields.%s }}", f.Name),
			UniqueID:          uniqueID,
			UnitOfMeasurement: f.Unit,
			DeviceClass:       dc,
			StateClass:        sc,
			Device: haDevice{
				Identifiers: []string{deviceID},
				Name:        deviceName,
			},
		}

		data, err := json.Marshal(payload)
		if err != nil {
			c.log.Warn("ha discovery marshal failed", "field", f.Name, "err", err)
			continue
		}

		topic := fmt.Sprintf("%s/sensor/%s/config", cfg.MQTTHADiscoveryPrefix, uniqueID)
		token := c.inner.Publish(topic, 0, true, data)
		if !token.WaitTimeout(5 * time.Second) {
			c.log.Warn("ha discovery publish timeout", "topic", topic)
			continue
		}
		if err := token.Error(); err != nil {
			c.log.Warn("ha discovery publish failed", "topic", topic, "err", err)
			continue
		}
		c.log.Debug("ha discovery published", "topic", topic, "field", f.Name)
	}
}

// haClassFromUnit returns the HA device_class and state_class for a given unit.
func haClassFromUnit(unit string) (deviceClass, stateClass string) {
	switch unit {
	case "°C", "K":
		return "temperature", "measurement"
	case "W", "kW":
		return "power", "measurement"
	case "Wh", "kWh":
		return "energy", "total_increasing"
	case "V":
		return "voltage", "measurement"
	case "bar":
		return "pressure", "measurement"
	case "l/h":
		return "volume_flow_rate", "measurement"
	default:
		return "", "measurement"
	}
}

// fieldSlug converts a field name to a lowercase ASCII slug safe for MQTT topics
// and HA unique_ids (e.g. "Temp S1" → "temp_s1").
func fieldSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func (c *Client) Disconnect() {
	c.inner.Disconnect(2000)
}
