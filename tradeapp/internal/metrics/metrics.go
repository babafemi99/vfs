// Package metrics is a small Prometheus-compatible registry: counters and
// histograms with labels, plus collectors that read live numbers (job queue
// depth, circuit state) at scrape time. It writes the Prometheus text
// format, so Prometheus, VictoriaMetrics or any compatible agent can scrape /metrics. Written by
// hand to keep the dependency list at pgx and ulid; swapping in
// prometheus/client_golang later only touches this package.
package metrics

import (
	"context"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Registry holds every metric and writes them out.
type Registry struct {
	mu         sync.Mutex
	metrics    []writer
	collectors []Collector
}

// Collector writes metrics computed at scrape time (gauges read from the
// database or from in-memory state).
type Collector func(ctx context.Context, w *Writer) error

type writer interface {
	write(w io.Writer)
}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) add(m writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metrics = append(r.metrics, m)
}

// AddCollector registers a scrape-time collector.
func (r *Registry) AddCollector(c Collector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.collectors = append(r.collectors, c)
}

// WriteTo writes every metric in the Prometheus text format. A failing
// collector is reported as scrape_collector_errors_total-style comment and
// skipped; the rest still get written.
func (r *Registry) WriteTo(ctx context.Context, out io.Writer) {
	r.mu.Lock()
	metrics := append([]writer(nil), r.metrics...)
	collectors := append([]Collector(nil), r.collectors...)
	r.mu.Unlock()

	for _, m := range metrics {
		m.write(out)
	}
	w := &Writer{out: out}
	failed := 0
	for _, c := range collectors {
		if err := c(ctx, w); err != nil {
			failed++
			fmt.Fprintf(out, "# collector error: %s\n", strings.ReplaceAll(err.Error(), "\n", " "))
		}
	}
	w.Gauge("metrics_collector_errors", "Collectors that failed during this scrape.", float64(failed), nil)
}

// Labels are label name → value pairs.
type Labels map[string]string

// ---------------------------------------------------------------- counter

// CounterVec is a counter per label combination.
type CounterVec struct {
	name, help string
	labels     []string
	mu         sync.Mutex
	values     map[string]*counterValue
}

type counterValue struct {
	labelValues []string
	v           float64
}

func (r *Registry) NewCounterVec(name, help string, labels ...string) *CounterVec {
	c := &CounterVec{name: name, help: help, labels: labels, values: map[string]*counterValue{}}
	r.add(c)
	return c
}

// Inc adds one for these label values (in the order the labels were declared).
func (c *CounterVec) Inc(labelValues ...string) { c.Add(1, labelValues...) }

func (c *CounterVec) Add(n float64, labelValues ...string) {
	if c == nil {
		return
	}
	k := strings.Join(labelValues, "\x00")
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.values[k]
	if !ok {
		v = &counterValue{labelValues: append([]string(nil), labelValues...)}
		c.values[k] = v
	}
	v.v += n
}

// Value returns the current count for these label values (for tests).
func (c *CounterVec) Value(labelValues ...string) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.values[strings.Join(labelValues, "\x00")]; ok {
		return v.v
	}
	return 0
}

func (c *CounterVec) write(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", c.name, c.help, c.name)
	for _, k := range sortedKeys(c.values) {
		v := c.values[k]
		fmt.Fprintf(w, "%s%s %s\n", c.name, formatLabels(c.labels, v.labelValues, "", ""), formatFloat(v.v))
	}
}

// -------------------------------------------------------------- histogram

// DefaultBuckets suit HTTP latencies in seconds.
var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// HistogramVec is a histogram per label combination.
type HistogramVec struct {
	name, help string
	labels     []string
	buckets    []float64
	mu         sync.Mutex
	values     map[string]*histogramValue
}

type histogramValue struct {
	labelValues []string
	counts      []uint64 // per bucket, not cumulative
	sum         float64
	count       uint64
}

func (r *Registry) NewHistogramVec(name, help string, buckets []float64, labels ...string) *HistogramVec {
	h := &HistogramVec{name: name, help: help, labels: labels, buckets: buckets, values: map[string]*histogramValue{}}
	r.add(h)
	return h
}

// Observe records one value (seconds, for durations).
func (h *HistogramVec) Observe(v float64, labelValues ...string) {
	if h == nil {
		return
	}
	k := strings.Join(labelValues, "\x00")
	h.mu.Lock()
	defer h.mu.Unlock()
	hv, ok := h.values[k]
	if !ok {
		hv = &histogramValue{labelValues: append([]string(nil), labelValues...), counts: make([]uint64, len(h.buckets))}
		h.values[k] = hv
	}
	for i, b := range h.buckets {
		if v <= b {
			hv.counts[i]++
			break
		}
	}
	hv.sum += v
	hv.count++
}

// ObserveSince records the time since start, in seconds.
func (h *HistogramVec) ObserveSince(start time.Time, labelValues ...string) {
	h.Observe(time.Since(start).Seconds(), labelValues...)
}

func (h *HistogramVec) write(w io.Writer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", h.name, h.help, h.name)
	for _, k := range sortedKeys(h.values) {
		hv := h.values[k]
		var cumulative uint64
		for i, b := range h.buckets {
			cumulative += hv.counts[i]
			fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, formatLabels(h.labels, hv.labelValues, "le", formatFloat(b)), cumulative)
		}
		fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, formatLabels(h.labels, hv.labelValues, "le", "+Inf"), hv.count)
		fmt.Fprintf(w, "%s_sum%s %s\n", h.name, formatLabels(h.labels, hv.labelValues, "", ""), formatFloat(hv.sum))
		fmt.Fprintf(w, "%s_count%s %d\n", h.name, formatLabels(h.labels, hv.labelValues, "", ""), hv.count)
	}
}

// ----------------------------------------------------------------- writer

// Writer is what collectors write scrape-time gauges with.
type Writer struct {
	out      io.Writer
	declared map[string]bool
}

// Gauge writes one gauge sample. Samples of the same name may be written
// several times with different labels; the header is written once.
func (w *Writer) Gauge(name, help string, v float64, labels Labels) {
	if w.declared == nil {
		w.declared = map[string]bool{}
	}
	if !w.declared[name] {
		fmt.Fprintf(w.out, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
		w.declared[name] = true
	}
	names := make([]string, 0, len(labels))
	for k := range labels {
		names = append(names, k)
	}
	sort.Strings(names)
	values := make([]string, len(names))
	for i, k := range names {
		values[i] = labels[k]
	}
	fmt.Fprintf(w.out, "%s%s %s\n", name, formatLabels(names, values, "", ""), formatFloat(v))
}

// ---------------------------------------------------------------- helpers

func formatLabels(names, values []string, extraName, extraValue string) string {
	if len(names) == 0 && extraName == "" {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		v := ""
		if i < len(values) {
			v = values[i]
		}
		fmt.Fprintf(&b, "%s=\"%s\"", n, escape(v))
	}
	if extraName != "" {
		if len(names) > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=\"%s\"", extraName, extraValue)
	}
	b.WriteByte('}')
	return b.String()
}

var escaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func escape(s string) string { return escaper.Replace(s) }

func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
