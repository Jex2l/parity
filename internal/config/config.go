// Package config loads and validates parity.yaml.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config mirrors the parity.yaml schema described in the product spec.
// Only Phase 1 fields (capture + scrub) are populated/validated for now;
// later phases add replay/analysis/privacy without breaking this shape.
type Config struct {
	Service string        `yaml:"service"`
	Capture CaptureConfig `yaml:"capture"`
	Scrub   ScrubConfig   `yaml:"scrub"`
}

type CaptureConfig struct {
	Listen      string             `yaml:"listen"`
	Upstream    string             `yaml:"upstream"`
	Sampling    SamplingConfig     `yaml:"sampling"`
	DenyPaths   []string           `yaml:"deny_paths"`
}

type SamplingConfig struct {
	DefaultRate float64            `yaml:"default_rate"`
	PerEndpoint map[string]float64 `yaml:"per_endpoint"`
	Interesting bool               `yaml:"interesting"`
}

type ScrubConfig struct {
	DefaultPolicy string            `yaml:"default_policy"`
	HeadersAllow  []string          `yaml:"headers_allow"`
	JSONPaths     map[string]string `yaml:"json_paths"`
	// HMACSecretEnv names an environment variable holding the per-tenant
	// pseudonymization secret (see internal/scrub). The secret itself is
	// never written here or anywhere in parity.yaml — only the name of
	// the env var that holds it — so a committed config file never leaks
	// the secret. If unset, the capture binary falls back to an insecure
	// well-known default and logs a loud warning; see cmd/parity-capture.
	HMACSecretEnv string `yaml:"hmac_secret_env"`
}

// Load reads and validates a parity.yaml file at path.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("config: invalid %s: %w", path, err)
	}
	return &c, nil
}

// Validate checks required fields and value ranges, returning a
// human-readable error naming the offending field.
func (c *Config) Validate() error {
	if c.Service == "" {
		return fmt.Errorf("service: required")
	}
	if c.Capture.Listen == "" {
		return fmt.Errorf("capture.listen: required")
	}
	if c.Capture.Upstream == "" {
		return fmt.Errorf("capture.upstream: required")
	}
	r := c.Capture.Sampling.DefaultRate
	if r < 0 || r > 1 {
		return fmt.Errorf("capture.sampling.default_rate: must be in [0,1], got %v", r)
	}
	for ep, rate := range c.Capture.Sampling.PerEndpoint {
		if rate < 0 || rate > 1 {
			return fmt.Errorf("capture.sampling.per_endpoint[%s]: must be in [0,1], got %v", ep, rate)
		}
	}
	switch c.Scrub.DefaultPolicy {
	case "", "preserve", "redact", "pseudonymize", "drop_request":
	default:
		return fmt.Errorf("scrub.default_policy: unknown policy %q", c.Scrub.DefaultPolicy)
	}
	return nil
}
