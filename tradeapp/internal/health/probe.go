// Package health is a tiny client for container health checks. The images
// are distroless (no shell, no curl), so the binary checks itself:
// `api healthcheck` exits 0 when GET /healthz answers 200.
package health

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Probe GETs path on the server listening at addr (":8091" or
// "host:8091") and returns nil on a 200.
func Probe(addr, path string) error {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Get("http://" + addr + path)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", path, res.Status)
	}
	return nil
}
