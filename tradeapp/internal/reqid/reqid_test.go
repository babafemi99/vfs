package reqid

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestLogHandlerAddsRequestID(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(NewLogHandler(slog.NewTextHandler(&buf, nil))).With("svc", "api")

	log.InfoContext(With(context.Background(), "abc123"), "with id")
	log.InfoContext(context.Background(), "without id")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if !strings.Contains(lines[0], "request_id=abc123") || !strings.Contains(lines[0], "svc=api") {
		t.Fatalf("first line: %s", lines[0])
	}
	if strings.Contains(lines[1], "request_id") {
		t.Fatalf("second line: %s", lines[1])
	}
	if Ptr(context.Background()) != nil || *Ptr(With(context.Background(), "x")) != "x" {
		t.Fatal("Ptr")
	}
}
