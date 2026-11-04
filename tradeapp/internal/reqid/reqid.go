// Package reqid carries a request id through context.Context and into logs.
//
// The HTTP layer puts the id in the context (X-Request-Id). Jobs queued by
// that request store it, and the worker puts it back in the context when it
// runs them, so one id follows a request from HTTP, through the service, to
// the background job and the provider call it makes.
package reqid

import (
	"context"
	"log/slog"
)

type key struct{}

// With returns ctx carrying id. An empty id leaves ctx as it is.
func With(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, key{}, id)
}

// From returns the request id in ctx, or "".
func From(ctx context.Context) string {
	id, _ := ctx.Value(key{}).(string)
	return id
}

// Ptr returns the id in ctx as a pointer, nil when there is none (for
// nullable columns).
func Ptr(ctx context.Context) *string {
	if id := From(ctx); id != "" {
		return &id
	}
	return nil
}

// LogHandler wraps a slog.Handler and adds request_id to every record logged
// with a context that carries one (log.InfoContext(ctx, ...)).
type LogHandler struct {
	slog.Handler
}

func NewLogHandler(h slog.Handler) *LogHandler { return &LogHandler{Handler: h} }

func (h *LogHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := From(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h *LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *LogHandler) WithGroup(name string) slog.Handler {
	return &LogHandler{Handler: h.Handler.WithGroup(name)}
}
