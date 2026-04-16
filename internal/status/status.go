// Package status provides a thread-safe runtime status container shared
// between the bridge main loop and the web UI handlers.
package status

import (
	"sync"
	"time"
)

// Status holds observable runtime state of the bridge.
type Status struct {
	mu            sync.RWMutex
	mqttConnected bool
	lastPublish   time.Time
	devices       []string
	startTime     time.Time
}

// Snapshot is the over-the-wire JSON shape returned by GET /api/status.
type Snapshot struct {
	MQTTConnected bool      `json:"mqtt_connected"`
	LastPublish   time.Time `json:"last_publish,omitempty"`
	Devices       []string  `json:"devices"`
	UptimeSeconds int64     `json:"uptime_seconds"`
}

func New() *Status {
	return &Status{startTime: time.Now()}
}

func (s *Status) SetMQTTConnected(v bool) {
	s.mu.Lock()
	s.mqttConnected = v
	s.mu.Unlock()
}

func (s *Status) SetLastPublish(t time.Time) {
	s.mu.Lock()
	s.lastPublish = t
	s.mu.Unlock()
}

func (s *Status) SetDevices(devices []string) {
	s.mu.Lock()
	cp := make([]string, len(devices))
	copy(cp, devices)
	s.devices = cp
	s.mu.Unlock()
}

func (s *Status) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	devices := make([]string, len(s.devices))
	copy(devices, s.devices)
	return Snapshot{
		MQTTConnected: s.mqttConnected,
		LastPublish:   s.lastPublish,
		Devices:       devices,
		UptimeSeconds: int64(time.Since(s.startTime).Seconds()),
	}
}
