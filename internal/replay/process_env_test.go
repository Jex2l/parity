package replay

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// buildHelperBinary compiles a tiny Go program from source so these
// tests exercise a real subprocess (real exec.Cmd, real exit codes),
// not a mock of process lifecycle.
func buildHelperBinary(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(srcPath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "helper")
	cmd := exec.Command("go", "build", "-o", binPath, srcPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building helper binary: %v\n%s", err, out)
	}
	return binPath
}

const healthyServerSrc = `package main

import ("net/http"; "os")

func main() {
	addr := os.Getenv("TEST_ADDR")
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	http.ListenAndServe(addr, nil)
}
`

const crashesImmediatelySrc = `package main

import "os"

func main() {
	os.Exit(1)
}
`

func TestProcessEnvironment_TeardownReapsAndIsIdempotent(t *testing.T) {
	bin := buildHelperBinary(t, healthyServerSrc)
	env := NewProcessEnvironment("TEST_ADDR", "/health")

	ep, err := env.Provision(context.Background(), BuildRef{Name: "helper", Cmd: bin})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if ep.BaseURL == "" {
		t.Fatalf("expected a non-empty endpoint")
	}

	done := make(chan struct{})
	go func() {
		env.Teardown(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Teardown did not return within 3s — likely a deadlock in process reaping")
	}

	// Calling Teardown again must not hang or panic (idempotent cleanup —
	// orchestrate.Run's deferred Teardown calls happen regardless of
	// whether an earlier explicit Teardown already ran in some code path).
	done2 := make(chan struct{})
	go func() {
		env.Teardown(context.Background())
		close(done2)
	}()
	select {
	case <-done2:
	case <-time.After(3 * time.Second):
		t.Fatal("second Teardown call did not return within 3s")
	}
}

func TestProcessEnvironment_DetectsEarlyCrashQuickly(t *testing.T) {
	bin := buildHelperBinary(t, crashesImmediatelySrc)
	env := NewProcessEnvironment("TEST_ADDR", "/health")

	start := time.Now()
	_, err := env.Provision(context.Background(), BuildRef{Name: "crasher", Cmd: bin})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected Provision to fail for a binary that exits immediately")
	}
	// Without early-exit detection this would take the full 5s
	// waitHealthy timeout; detecting the exit should make it fail fast.
	if elapsed > 2*time.Second {
		t.Fatalf("Provision took %v to notice the process had already exited; expected well under the 5s health-check timeout", elapsed)
	}
	t.Logf("early crash detected in %v: %v", elapsed, err)

	// Teardown after a failed Provision must still be safe.
	done := make(chan struct{})
	go func() {
		env.Teardown(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Teardown after a failed Provision did not return within 3s")
	}
}
