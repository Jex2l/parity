package replay

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
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
}

// NewProcessEnvironment creates a ProcessEnvironment. portEnvVar is the
// environment variable the target binary reads its bind address from
// (Parity sets it to ":<free-port>"); healthPath is polled after start to
// detect readiness.
func NewProcessEnvironment(portEnvVar, healthPath string) *ProcessEnvironment {
	return &ProcessEnvironment{PortEnvVar: portEnvVar, HealthPath: healthPath}
}

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

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitHealthy(base+e.HealthPath, 5*time.Second); err != nil {
		_ = cmd.Process.Kill()
		return Endpoint{}, fmt.Errorf("replay: %s did not become healthy: %w", build.Name, err)
	}
	return Endpoint{BaseURL: base}, nil
}

func waitHealthy(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var lastErr error
	for time.Now().Before(deadline) {
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

// Teardown kills the subprocess.
func (e *ProcessEnvironment) Teardown(ctx context.Context) error {
	if e.cmd == nil || e.cmd.Process == nil {
		return nil
	}
	return e.cmd.Process.Kill()
}
