package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Store is a thread-safe, file-persistent configuration container.
// Callers use Version() to detect changes without a channel.
type Store struct {
	mu      sync.RWMutex
	cfg     Config
	path    string
	version atomic.Uint64
}

// NewStore initialises from env vars, then overlays any saved file at path.
func NewStore(path string) *Store {
	s := &Store{
		cfg:  *Load(),
		path: path,
	}
	if path != "" {
		_ = s.loadFile() // file may not exist yet – that's fine
	}
	return s
}

// Get returns a snapshot of the current configuration.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Version returns a monotonically increasing counter that increments on every
// successful Update call. Use it to detect config changes without polling.
func (s *Store) Version() uint64 {
	return s.version.Load()
}

// Update atomically applies fn to a copy of the current config, persists it
// to disk (if a path is set), and increments the version counter.
func (s *Store) Update(fn func(*Config)) error {
	s.mu.Lock()
	next := s.cfg
	fn(&next)
	s.cfg = next
	s.mu.Unlock()

	s.version.Add(1)

	if s.path != "" {
		return s.saveFile()
	}
	return nil
}

// ─── file persistence ─────────────────────────────────────────────────────────

// persisted is the on-disk JSON representation of Config.
// Using pointer fields for booleans/bytes so we can distinguish
// "not set" from "set to zero/false".
type persisted struct {
	SerialPort      string `json:"serial_port,omitempty"`
	BaudRate        int    `json:"baud_rate,omitempty"`
	MQTTBroker      string `json:"mqtt_broker,omitempty"`
	MQTTTopicPrefix string `json:"mqtt_topic_prefix,omitempty"`
	MQTTUser        string `json:"mqtt_user,omitempty"`
	MQTTPass        string `json:"mqtt_pass,omitempty"`
	MQTTRetain      *bool  `json:"mqtt_retain,omitempty"`
	MQTTQOS         *byte  `json:"mqtt_qos,omitempty"`
	PublishInterval string `json:"publish_interval,omitempty"`
	LogLevel        string `json:"log_level,omitempty"`
	LogFormat       string `json:"log_format,omitempty"`
}

func (s *Store) loadFile() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var p persisted
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.SerialPort != "" {
		s.cfg.SerialPort = p.SerialPort
	}
	if p.BaudRate != 0 {
		s.cfg.BaudRate = p.BaudRate
	}
	if p.MQTTBroker != "" {
		s.cfg.MQTTBroker = p.MQTTBroker
	}
	if p.MQTTTopicPrefix != "" {
		s.cfg.MQTTTopicPrefix = p.MQTTTopicPrefix
	}
	if p.MQTTUser != "" {
		s.cfg.MQTTUser = p.MQTTUser
	}
	if p.MQTTPass != "" {
		s.cfg.MQTTPass = p.MQTTPass
	}
	if p.MQTTRetain != nil {
		s.cfg.MQTTRetain = *p.MQTTRetain
	}
	if p.MQTTQOS != nil {
		s.cfg.MQTTQOS = *p.MQTTQOS
	}
	if p.PublishInterval != "" {
		if d, err := time.ParseDuration(p.PublishInterval); err == nil {
			s.cfg.PublishInterval = d
		}
	}
	if p.LogLevel != "" {
		s.cfg.LogLevel = p.LogLevel
	}
	if p.LogFormat != "" {
		s.cfg.LogFormat = p.LogFormat
	}
	return nil
}

func (s *Store) saveFile() error {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	retain := cfg.MQTTRetain
	qos := cfg.MQTTQOS
	p := persisted{
		SerialPort:      cfg.SerialPort,
		BaudRate:        cfg.BaudRate,
		MQTTBroker:      cfg.MQTTBroker,
		MQTTTopicPrefix: cfg.MQTTTopicPrefix,
		MQTTUser:        cfg.MQTTUser,
		MQTTPass:        cfg.MQTTPass,
		MQTTRetain:      &retain,
		MQTTQOS:         &qos,
		PublishInterval: cfg.PublishInterval.String(),
		LogLevel:        cfg.LogLevel,
		LogFormat:       cfg.LogFormat,
	}

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}

	// Atomic write: write to .tmp then rename.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
