package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTmp(t *testing.T, content string) string {
	dir := t.TempDir()
	path := filepath.Join(dir, "parity.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_Valid(t *testing.T) {
	path := writeTmp(t, `
service: orders-api
capture:
  listen: ":8080"
  upstream: "http://localhost:3000"
  sampling:
    default_rate: 0.1
    per_endpoint:
      /health: 0
    interesting: true
  deny_paths: ["/health", "/metrics"]
scrub:
  default_policy: pseudonymize
  headers_allow: [content-type]
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Service != "orders-api" {
		t.Errorf("Service = %q", c.Service)
	}
	if c.Capture.Sampling.DefaultRate != 0.1 {
		t.Errorf("DefaultRate = %v", c.Capture.Sampling.DefaultRate)
	}
}

func TestLoad_InvalidRate(t *testing.T) {
	path := writeTmp(t, `
service: orders-api
capture:
  listen: ":8080"
  upstream: "http://localhost:3000"
  sampling:
    default_rate: 1.5
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for out-of-range rate")
	}
}

func TestLoad_MissingService(t *testing.T) {
	path := writeTmp(t, `
capture:
  listen: ":8080"
  upstream: "http://localhost:3000"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing service")
	}
}
