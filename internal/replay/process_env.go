package replay

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// ProcessEnvironment runs a BuildRef as a local subprocess bound to a free
// TCP port, used in place of a Docker Compose/Kubernetes environment where
// no container runtime is available (see docs/decisions/0002). It is a
// legitimate Environment implementation, not a mock: a real build command
// is actually executed and actually serves the replayed requests.
type ProcessEnvironment struct {
	PortEnvVar string // env var name the subprocess reads its listen port from, e.g. "DEMO_ADDR" expects ":PORT"
	HealthPath string // path polled until it returns any HTTP response, to know the process is ready

	cmd  *exec.Cmd
	port int

	// reap deduplicates process cleanup: exactly one goroutine ever calls
	// cmd.Wait() (calling it twice is a documented bug in os/exec), and
	// both the Provision failure path and Teardown can trigger it, so
	// whichever gets there first does the real work and everyone else
	// just waits on reapDone.
	reapOnce sync.Once
	reapDone chan struct{}
	waitErr  error
}

// NewProcessEnvironment creates a ProcessEnvironment. portEnvVar is the
// environment variable the target binary reads its bind address from
// (Parity sets it to ":<free-port>"); healthPath is polled after start to
// detect readiness.
func NewProcessEnvironment(portEnvVar, healthPath string) *ProcessEnvironment {
	return &ProcessEnvironment{PortEnvVar: portEnvVar, HealthPath: healthPath}
}

// freePort finds a currently-unused TCP port by binding to port 0 and
// immediately releasing it. This has an inherent, unavoidable
// TOCTOU race in any "find a free port, then let someone else bind it"
// scheme — something else could grab the port in the gap between this
// Close and the subprocess's own bind. It's accepted here (consistent
// with how most local dev tooling allocates ports) rather than solved by
// passing a pre-bound listener fd into the child process, which would be
// considerably more invasive for a replay-only tool. If flakiness from
// this race is ever observed in practice, that's the fix to reach for.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Provision starts build.Cmd as a subprocess bound to a free port and
// waits until it answers HTTP requests.
func (e *ProcessEnvironment) Provision(ctx context.Context, build BuildRef) (Endpoint, error) {
	port, err := freePort()
	if err != nil {
		return Endpoint{}, fmt.Errorf("replay: allocate port: %w", err)
	}
	e.port = port

	cmd := exec.CommandContext(ctx, build.Cmd, build.Args...)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("%s=:%s", e.PortEnvVar, strconv.Itoa(port)))
	for k, v := range build.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Stdout = os.Stderr // subprocess logs go to our stderr, never mixed into report output
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return Endpoint{}, fmt.Errorf("replay: start %s: %w", build.Name, err)
	}
	e.cmd = cmd
	e.reapDone = make(chan struct{})

	// Exactly one call to cmd.Wait() ever happens, in this goroutine,
	// regardless of whether the process becomes healthy, crashes early,
	// or is later killed by Teardown — see reap().
	go func() {
		e.waitErr = cmd.Wait()
		close(e.reapDone)
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	healthErr := waitHealthyOrExit(base+e.HealthPath, 5*time.Second, e.reapDone)
	if healthErr != nil {
		e.reap() // the process either already exited or needs killing; either way, reap it
		return Endpoint{}, fmt.Errorf("replay: %s did not become healthy: %w", build.Name, healthErr)
	}
	return Endpoint{BaseURL: base}, nil
}

// waitHealthyOrExit polls url until it responds, the timeout elapses, or
// reapDone fires first (the subprocess died on its own), whichever comes
// first.
func waitHealthyOrExit(url string, timeout time.Duration, reapDone <-chan struct{}) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case <-reapDone:
			return fmt.Errorf("process exited before becoming healthy")
		default:
		}
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s: %w", url, lastErr)
}

// Reset is a no-op for ProcessEnvironment (the demo service is stateless
// in Phase 2; database snapshot restore is a later-phase concern).
func (e *ProcessEnvironment) Reset(ctx context.Context) error { return nil }

// Teardown kills the subprocess and reaps it. Killing without reaping
// (Wait) leaves a zombie process on Unix until the parent itself exits —
// harmless for a single one-shot CLI invocation, but a real cost in a
// long-running process (the MCP server, a control plane) that provisions
// many short-lived replay environments over its lifetime. Safe to call
// even if the process already exited on its own (e.g. it crashed before
// Teardown was ever reached).
func (e *ProcessEnvironment) Teardown(ctx context.Context) error {
	if e.cmd == nil || e.cmd.Process == nil {
		return nil
	}
	e.reap()
	// os/exec reports "process already finished" if it had already
	// exited by the time we got here — not a real failure for a
	// best-effort Teardown, so it's deliberately not surfaced as an
	// error; e.waitErr (from the one real Wait() call) is the one that
	// matters, and it's merely informational here (an environment
	// that's been torn down is expected to have a non-zero/killed exit).
	return nil
}

// reap kills the process (best-effort — it may have already exited) and
// blocks until the single background Wait() call has completed, however
// it was triggered. Safe to call more than once or concurrently.
func (e *ProcessEnvironment) reap() {
	e.reapOnce.Do(func() {
		_ = e.cmd.Process.Kill()
	})
	<-e.reapDone
}
