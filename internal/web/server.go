// Package web provides the HTTP settings UI and REST API for vbus2mqtt.
package web

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"git.zk35.de/secalpha/vbus2mqtt/internal/config"
	"git.zk35.de/secalpha/vbus2mqtt/internal/status"
)

//go:embed ui.html
var uiHTML []byte

// configDTO is the over-the-wire representation used by GET/PUT /api/config.
// Passwords are redacted in GET responses; an empty string in PUT means "keep existing".
type configDTO struct {
	SerialPort      string `json:"serial_port"`
	BaudRate        int    `json:"baud_rate"`
	MQTTBroker      string `json:"mqtt_broker"`
	MQTTTopicPrefix string `json:"mqtt_topic_prefix"`
	MQTTUser        string `json:"mqtt_user"`
	MQTTPass        string `json:"mqtt_pass"` // "****" in GET if set; ignored in PUT if "****" or ""
	MQTTRetain      bool   `json:"mqtt_retain"`
	MQTTQOS         int    `json:"mqtt_qos"`
	PublishInterval string `json:"publish_interval"`
	LogLevel        string `json:"log_level"`
	LogFormat       string `json:"log_format"`
}

// Server is the HTTP settings server.
type Server struct {
	store   *config.Store
	status  *status.Status
	version string
	log     *slog.Logger
	webUser string
	webPass string
}

func New(store *config.Store, st *status.Status, version string, log *slog.Logger) *Server {
	cfg := store.Get()
	return &Server{
		store:   store,
		status:  st,
		version: version,
		log:     log,
		webUser: cfg.WebUser,
		webPass: cfg.WebPass,
	}
}

// basicAuth wraps h with HTTP Basic Auth. /health is always excluded.
func (s *Server) basicAuth(h http.HandlerFunc) http.HandlerFunc {
	if s.webUser == "" {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		userMatch := subtle.ConstantTimeCompare([]byte(u), []byte(s.webUser)) == 1
		passMatch := subtle.ConstantTimeCompare([]byte(p), []byte(s.webPass)) == 1
		if !ok || !userMatch || !passMatch {
			w.Header().Set("WWW-Authenticate", `Basic realm="vbus2mqtt"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// Start runs the HTTP server until ctx is cancelled.
func (s *Server) Start(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.basicAuth(s.handleUI))
	mux.HandleFunc("GET /health", s.handleHealth) // always public – used by HEALTHCHECK
	mux.HandleFunc("GET /api/config", s.basicAuth(s.handleGetConfig))
	mux.HandleFunc("PUT /api/config", s.basicAuth(s.handlePutConfig))
	mux.HandleFunc("GET /api/status", s.basicAuth(s.handleGetStatus))

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	s.log.Info("web UI listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// ─── handlers ────────────────────────────────────────────────────────────────

func (s *Server) handleUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(uiHTML)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": s.version,
	})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := s.store.Get()
	dto := configDTO{
		SerialPort:      cfg.SerialPort,
		BaudRate:        cfg.BaudRate,
		MQTTBroker:      cfg.MQTTBroker,
		MQTTTopicPrefix: cfg.MQTTTopicPrefix,
		MQTTUser:        cfg.MQTTUser,
		MQTTRetain:      cfg.MQTTRetain,
		MQTTQOS:         int(cfg.MQTTQOS),
		PublishInterval: cfg.PublishInterval.String(),
		LogLevel:        cfg.LogLevel,
		LogFormat:       cfg.LogFormat,
	}
	if cfg.MQTTPass != "" {
		dto.MQTTPass = "****"
	}
	writeJSON(w, http.StatusOK, dto)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var dto configDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	interval, err := time.ParseDuration(dto.PublishInterval)
	if err != nil || interval < time.Second {
		http.Error(w, "publish_interval must be a valid Go duration >= 1s (e.g. '30s')", http.StatusBadRequest)
		return
	}
	if dto.MQTTQOS < 0 || dto.MQTTQOS > 2 {
		http.Error(w, "mqtt_qos must be 0, 1, or 2", http.StatusBadRequest)
		return
	}
	switch dto.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		http.Error(w, "log_level must be debug|info|warn|error", http.StatusBadRequest)
		return
	}
	switch dto.LogFormat {
	case "json", "text":
	default:
		http.Error(w, "log_format must be json|text", http.StatusBadRequest)
		return
	}

	if err := s.store.Update(func(c *config.Config) {
		c.SerialPort = dto.SerialPort
		c.BaudRate = dto.BaudRate
		c.MQTTBroker = dto.MQTTBroker
		c.MQTTTopicPrefix = dto.MQTTTopicPrefix
		c.MQTTUser = dto.MQTTUser
		// Keep existing password when client sends "" or "****".
		if dto.MQTTPass != "" && dto.MQTTPass != "****" {
			c.MQTTPass = dto.MQTTPass
		}
		c.MQTTRetain = dto.MQTTRetain
		c.MQTTQOS = byte(dto.MQTTQOS)
		c.PublishInterval = interval
		c.LogLevel = dto.LogLevel
		c.LogFormat = dto.LogFormat
	}); err != nil {
		s.log.Error("config save failed", "err", err)
		http.Error(w, "failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.log.Info("config updated via web UI")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGetStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.status.Snapshot())
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
