package metrics

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTextFormat(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("things_total", "Things.", "kind")
	c.Inc("a")
	c.Add(2, `we"ird`)
	h := r.NewHistogramVec("lat_seconds", "Latency.", []float64{0.1, 1}, "route")
	h.Observe(0.05, "/x")
	h.Observe(0.5, "/x")
	h.Observe(3, "/x")
	r.AddCollector(func(ctx context.Context, w *Writer) error {
		w.Gauge("queue", "Queue.", 4, Labels{"status": "pending"})
		w.Gauge("queue", "Queue.", 1, Labels{"status": "dead"})
		return nil
	})
	r.AddCollector(func(ctx context.Context, w *Writer) error { return errors.New("db down") })

	var buf bytes.Buffer
	r.WriteTo(context.Background(), &buf)
	out := buf.String()

	for _, want := range []string{
		"# TYPE things_total counter\n",
		`things_total{kind="a"} 1` + "\n",
		`things_total{kind="we\"ird"} 2` + "\n",
		"# TYPE lat_seconds histogram\n",
		`lat_seconds_bucket{route="/x",le="0.1"} 1` + "\n",
		`lat_seconds_bucket{route="/x",le="1"} 2` + "\n",
		`lat_seconds_bucket{route="/x",le="+Inf"} 3` + "\n",
		`lat_seconds_sum{route="/x"} 3.55` + "\n",
		`lat_seconds_count{route="/x"} 3` + "\n",
		`queue{status="pending"} 4` + "\n",
		`queue{status="dead"} 1` + "\n",
		"# collector error: db down\n",
		"metrics_collector_errors 1\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Count(out, "# TYPE queue gauge") != 1 {
		t.Error("gauge header written more than once")
	}
	if c.Value("a") != 1 {
		t.Error("Value")
	}
}
