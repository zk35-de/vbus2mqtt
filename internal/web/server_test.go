package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.zk35.de/secalpha/vbus2mqtt/internal/config"
	"git.zk35.de/secalpha/vbus2mqtt/internal/status"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	store := config.NewStore("")
	st := status.New()
	return New(store, st, "test", nil_logger())
}

func newTestServerWithAuth(t *testing.T, user, pass string) *Server {
	t.Helper()
	store := config.NewStore("")
	_ = store.Update(func(c *config.Config) {
		c.WebUser = user
		c.WebPass = pass
	})
	st := status.New()
	srv := New(store, st, "test", nil_logger())
	srv.webUser = user
	srv.webPass = pass
	return srv
}

func nil_logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discard{}, &slog.HandlerOptions{Level: slog.LevelError + 10}))
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// ─── /health ─────────────────────────────────────────────────────────────────

func TestHealth_AlwaysPublic(t *testing.T) {
	srv := newTestServerWithAuth(t, "admin", "secret")
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.handleHealth(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	_ = json.NewDecoder(w.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("unexpected body: %v", body)
	}
	if body["version"] != "test" {
		t.Errorf("version: %q", body["version"])
	}
}

// ─── Basic Auth ───────────────────────────────────────────────────────────────

func TestBasicAuth_NoAuthWhenUserEmpty(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	srv.basicAuth(srv.handleGetConfig)(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 without auth configured, got %d", w.Code)
	}
}

func TestBasicAuth_RejectsNoCredentials(t *testing.T) {
	srv := newTestServerWithAuth(t, "admin", "secret")
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	srv.basicAuth(srv.handleGetConfig)(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestBasicAuth_RejectsWrongPassword(t *testing.T) {
	srv := newTestServerWithAuth(t, "admin", "secret")
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.SetBasicAuth("admin", "wrong")
	w := httptest.NewRecorder()
	srv.basicAuth(srv.handleGetConfig)(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestBasicAuth_AcceptsCorrectCredentials(t *testing.T) {
	srv := newTestServerWithAuth(t, "admin", "secret")
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.SetBasicAuth("admin", "secret")
	w := httptest.NewRecorder()
	srv.basicAuth(srv.handleGetConfig)(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ─── GET /api/config ─────────────────────────────────────────────────────────

func TestGetConfig_PasswordRedacted(t *testing.T) {
	store := config.NewStore("")
	_ = store.Update(func(c *config.Config) { c.MQTTPass = "hunter2" })
	srv := &Server{store: store, status: status.New(), version: "test", log: nil_logger()}
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	srv.handleGetConfig(w, req)
	var dto configDTO
	_ = json.NewDecoder(w.Body).Decode(&dto)
	if dto.MQTTPass != "****" {
		t.Errorf("expected redacted password, got %q", dto.MQTTPass)
	}
}

// ─── PUT /api/config ─────────────────────────────────────────────────────────

func TestPutConfig_ValidUpdate(t *testing.T) {
	srv := newTestServer(t)
	body := `{"serial_port":"/dev/ttyUSB0","baud_rate":9600,"mqtt_broker":"tcp://broker:1883","mqtt_topic_prefix":"vbus","mqtt_qos":1,"mqtt_retain":true,"publish_interval":"60s","log_level":"debug","log_format":"text"}`
	req := httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handlePutConfig(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if srv.store.Get().LogLevel != "debug" {
		t.Error("config not updated")
	}
}

func TestPutConfig_InvalidInterval(t *testing.T) {
	srv := newTestServer(t)
	body := `{"publish_interval":"100ms","log_level":"info","log_format":"json","mqtt_qos":0}`
	req := httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handlePutConfig(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestPutConfig_PasswordKeptWhenSentAsAsterisks(t *testing.T) {
	store := config.NewStore("")
	_ = store.Update(func(c *config.Config) { c.MQTTPass = "original" })
	srv := &Server{store: store, status: status.New(), version: "test", log: nil_logger()}

	body := `{"publish_interval":"30s","log_level":"info","log_format":"json","mqtt_qos":0,"mqtt_pass":"****"}`
	req := httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handlePutConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if store.Get().MQTTPass != "original" {
		t.Errorf("password should not have changed, got %q", store.Get().MQTTPass)
	}
}
