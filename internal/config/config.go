package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration, populated from environment variables.
type Config struct {
	// Serial device
	SerialPort string // SERIAL_PORT – empty = auto-detect
	BaudRate   int    // SERIAL_BAUD – default 9600

	// MQTT
	MQTTBroker      string // MQTT_BROKER      – default tcp://localhost:1883
	MQTTTopicPrefix string // MQTT_TOPIC_PREFIX – default vbus
	MQTTUser        string // MQTT_USER
	MQTTPass        string // MQTT_PASS
	MQTTRetain      bool   // MQTT_RETAIN      – default true
	MQTTQOS         byte   // MQTT_QOS         – default 0

	// Application
	PublishInterval time.Duration // PUBLISH_INTERVAL – default 30s
	LogLevel        string        // LOG_LEVEL  – debug|info|warn|error, default info
	LogFormat       string        // LOG_FORMAT – json|text, default json
}

func Load() *Config {
	return &Config{
		SerialPort:      env("SERIAL_PORT", ""),
		BaudRate:        envInt("SERIAL_BAUD", 9600),
		MQTTBroker:      env("MQTT_BROKER", "tcp://localhost:1883"),
		MQTTTopicPrefix: env("MQTT_TOPIC_PREFIX", "vbus"),
		MQTTUser:        env("MQTT_USER", ""),
		MQTTPass:        env("MQTT_PASS", ""),
		MQTTRetain:      envBool("MQTT_RETAIN", true),
		MQTTQOS:         byte(envInt("MQTT_QOS", 0)),
		PublishInterval: envDuration("PUBLISH_INTERVAL", 30*time.Second),
		LogLevel:        env("LOG_LEVEL", "info"),
		LogFormat:       env("LOG_FORMAT", "json"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
