package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/zk35-de/vbus2mqtt/internal/config"
)

// dynamicHandler delegates to json or text handler based on the current store
// config, allowing log_format changes to take effect without a restart.
type dynamicHandler struct {
	store *config.Store
	json  slog.Handler
	text  slog.Handler
	level *slog.LevelVar
}

func newDynamicHandler(store *config.Store, lv *slog.LevelVar) *dynamicHandler {
	opts := &slog.HandlerOptions{Level: lv}
	return &dynamicHandler{
		store: store,
		json:  slog.NewJSONHandler(os.Stdout, opts),
		text:  slog.NewTextHandler(os.Stdout, opts),
		level: lv,
	}
}

func (h *dynamicHandler) active() slog.Handler {
	if h.store.Get().LogFormat == "text" {
		return h.text
	}
	return h.json
}

func (h *dynamicHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.active().Enabled(ctx, l)
}

func (h *dynamicHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.active().Handle(ctx, r)
}

func (h *dynamicHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &dynamicHandler{
		store: h.store,
		json:  h.json.WithAttrs(attrs),
		text:  h.text.WithAttrs(attrs),
		level: h.level,
	}
}

func (h *dynamicHandler) WithGroup(name string) slog.Handler {
	return &dynamicHandler{
		store: h.store,
		json:  h.json.WithGroup(name),
		text:  h.text.WithGroup(name),
		level: h.level,
	}
}
