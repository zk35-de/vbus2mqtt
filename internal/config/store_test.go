package config_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zk35-de/vbus2mqtt/internal/config"
)

func TestStore_GetDefaults(t *testing.T) {
	s := config.NewStore("")
	cfg := s.Get()
	if cfg.MQTTBroker == "" {
		t.Error("expected non-empty default MQTTBroker")
	}
	if cfg.PublishInterval == 0 {
		t.Error("expected non-zero default PublishInterval")
	}
	if cfg.BaudRate == 0 {
		t.Error("expected non-zero default BaudRate")
	}
}

func TestStore_UpdateAndGet(t *testing.T) {
	s := config.NewStore("")
	err := s.Update(func(c *config.Config) {
		c.MQTTTopicPrefix = "test-prefix"
		c.LogLevel = "debug"
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	cfg := s.Get()
	if cfg.MQTTTopicPrefix != "test-prefix" {
		t.Errorf("MQTTTopicPrefix: got %q, want %q", cfg.MQTTTopicPrefix, "test-prefix")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel: got %q, want %q", cfg.LogLevel, "debug")
	}
}

func TestStore_VersionIncrements(t *testing.T) {
	s := config.NewStore("")
	v0 := s.Version()
	_ = s.Update(func(c *config.Config) { c.LogLevel = "debug" })
	v1 := s.Version()
	_ = s.Update(func(c *config.Config) { c.LogLevel = "info" })
	v2 := s.Version()

	if v1 <= v0 {
		t.Errorf("version did not increase after first update: %d → %d", v0, v1)
	}
	if v2 <= v1 {
		t.Errorf("version did not increase after second update: %d → %d", v1, v2)
	}
}

func TestStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	s1 := config.NewStore(path)
	_ = s1.Update(func(c *config.Config) {
		c.MQTTTopicPrefix = "persisted-prefix"
		c.PublishInterval = 10 * time.Second
		c.MQTTRetain = false
	})

	// New store from same file – should restore saved values.
	s2 := config.NewStore(path)
	cfg := s2.Get()
	if cfg.MQTTTopicPrefix != "persisted-prefix" {
		t.Errorf("MQTTTopicPrefix: got %q, want %q", cfg.MQTTTopicPrefix, "persisted-prefix")
	}
	if cfg.PublishInterval != 10*time.Second {
		t.Errorf("PublishInterval: got %v, want %v", cfg.PublishInterval, 10*time.Second)
	}
	if cfg.MQTTRetain != false {
		t.Error("MQTTRetain: expected false")
	}
}

func TestStore_PersistenceAtomicWrite(t *testing.T) {
	// Verify the .tmp file is cleaned up after successful save.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	s := config.NewStore(path)
	_ = s.Update(func(c *config.Config) { c.LogLevel = "warn" })

	tmp := path + ".tmp"
	if _, err := filepath.Glob(tmp); err == nil {
		// glob succeeded means pattern is valid, but file should not exist
	}
	// The .tmp file should have been renamed away.
	matches, _ := filepath.Glob(tmp)
	if len(matches) > 0 {
		t.Error("expected .tmp file to be removed after atomic save")
	}
}

func TestStore_PasswordNotLostOnReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	s1 := config.NewStore(path)
	_ = s1.Update(func(c *config.Config) {
		c.MQTTPass = "secret123"
	})

	s2 := config.NewStore(path)
	if s2.Get().MQTTPass != "secret123" {
		t.Error("MQTT password not persisted")
	}
}
