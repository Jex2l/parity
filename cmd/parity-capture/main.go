// Command parity-capture runs the capture reverse proxy standalone (as a
// sidecar or front-of-service proxy), per parity.yaml.
package main

import (
	"flag"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/jex2l/parity/internal/capture"
	"github.com/jex2l/parity/internal/config"
	"github.com/jex2l/parity/internal/scrub"
)

func main() {
	cfgPath := flag.String("config", "parity.yaml", "path to parity.yaml")
	segDir := flag.String("segments", "./segments", "directory to write recording segments to")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("parity-capture: %v", err)
	}

	upstream, err := url.Parse(cfg.Capture.Upstream)
	if err != nil {
		log.Fatalf("parity-capture: invalid upstream %q: %v", cfg.Capture.Upstream, err)
	}

	templater := capture.NewTemplater()
	sampler := capture.NewCompositeSampler(capture.Config{
		DefaultRate: cfg.Capture.Sampling.DefaultRate,
		PerEndpoint: cfg.Capture.Sampling.PerEndpoint,
		Interesting: cfg.Capture.Sampling.Interesting,
	})

	proxy := capture.NewProxy(capture.Options{
		Upstream:  upstream,
		Templater: templater,
		Sampler:   sampler,
		DenyPaths: cfg.Capture.DenyPaths,
		Service:   cfg.Service,
	})

	var hmacSecret []byte
	if cfg.Scrub.HMACSecretEnv != "" {
		if v := os.Getenv(cfg.Scrub.HMACSecretEnv); v != "" {
			hmacSecret = []byte(v)
		} else {
			slog.Warn("scrub.hmac_secret_env is set but the named environment variable is empty or unset; falling back to the insecure default pseudonymization secret", "env_var", cfg.Scrub.HMACSecretEnv)
		}
	}
	if len(hmacSecret) == 0 {
		slog.Warn("no scrub.hmac_secret_env configured (or it resolved empty): using scrub's insecure, well-known default pseudonymization secret. Pseudonymized values in captured segments are NOT safe from an attacker who knows this default. Set scrub.hmac_secret_env in parity.yaml and export that variable with a real per-tenant secret before using this in anything but local development.")
	}
	scrubber := scrub.New(scrub.Config{HeadersAllow: cfg.Scrub.HeadersAllow, HMACSecret: hmacSecret})
	sink, err := capture.NewScrubbingSink(*segDir, scrubber, 0)
	if err != nil {
		log.Fatalf("parity-capture: %v", err)
	}
	stop := proxy.Run(sink)

	srv := &http.Server{Addr: cfg.Capture.Listen, Handler: proxy}
	go func() {
		slog.Info("parity-capture listening", "addr", cfg.Capture.Listen, "upstream", cfg.Capture.Upstream, "segments", *segDir)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("parity-capture: serve: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	stop()
	sink.Close()
	srv.Close()
	slog.Info("parity-capture shut down", "captured", proxy.Metrics.Captured, "dropped", proxy.Metrics.Dropped)
}
