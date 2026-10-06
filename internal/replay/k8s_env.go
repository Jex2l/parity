package replay

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// KubernetesEnvironment provisions a build as an ephemeral Pod+Service in
// a Kubernetes namespace, using `kubectl` as a subprocess (boring tech,
// no client-go dependency to vendor). It satisfies the same Environment
// interface as ProcessEnvironment so callers don't care which backend
// runs underneath.
//
// IMPORTANT (see docs/plans/phase-5.md and docs/limits.md): this dev
// environment has no Kubernetes cluster. Not one kubectl command here
// has ever been run against a real cluster. This is reviewed, intended-
// to-be-correct code, not a verified implementation — treat it as a
// draft to validate before relying on it.
type KubernetesEnvironment struct {
	Namespace string
	Kubectl   string // path to kubectl; defaults to "kubectl" on PATH

	podName string
}

// NewKubernetesEnvironment creates a KubernetesEnvironment targeting
// namespace. Each Provision call creates a new, uniquely-named pod.
func NewKubernetesEnvironment(namespace string) *KubernetesEnvironment {
	return &KubernetesEnvironment{Namespace: namespace, Kubectl: "kubectl"}
}

func (e *KubernetesEnvironment) kubectlCmd(ctx context.Context, args ...string) *exec.Cmd {
	full := append([]string{"-n", e.Namespace}, args...)
	return exec.CommandContext(ctx, e.binary(), full...)
}

func (e *KubernetesEnvironment) binary() string {
	if e.Kubectl == "" {
		return "kubectl"
	}
	return e.Kubectl
}

// Provision creates a pod running build.Cmd as its entrypoint (via a
// `kubectl run` one-liner — adequate for ephemeral per-PR replay pods;
// a real Helm-deployed workload would use a proper Deployment manifest,
// see deploy/helm/) and port-forwards to it so the returned Endpoint is
// reachable from the machine running `parity replay`.
func (e *KubernetesEnvironment) Provision(ctx context.Context, build BuildRef) (Endpoint, error) {
	e.podName = fmt.Sprintf("parity-%s-%d", sanitizePodName(build.Name), time.Now().UnixNano())

	runArgs := []string{
		"run", e.podName,
		"--image=" + build.Cmd, // for k8s, BuildRef.Cmd is treated as an image reference, not a local binary
		"--restart=Never",
	}
	for k, v := range build.Env {
		runArgs = append(runArgs, "--env="+k+"="+v)
	}
	if out, err := e.kubectlCmd(ctx, runArgs...).CombinedOutput(); err != nil {
		return Endpoint{}, fmt.Errorf("replay: kubectl run %s: %w: %s", e.podName, err, out)
	}

	waitArgs := []string{"wait", "--for=condition=Ready", "pod/" + e.podName, "--timeout=60s"}
	if out, err := e.kubectlCmd(ctx, waitArgs...).CombinedOutput(); err != nil {
		return Endpoint{}, fmt.Errorf("replay: pod %s never became ready: %w: %s", e.podName, err, out)
	}

	// Port-forward a local port to the pod's port 8080 (the convention
	// this repo's demo services use); a real deployment would instead
	// create a ClusterIP Service and resolve via the cluster DNS name,
	// which is what deploy/helm/ assumes — the port-forward here is the
	// simplest thing that could let `parity replay` itself (running
	// outside the cluster) reach the pod.
	localPort, err := freePort()
	if err != nil {
		return Endpoint{}, fmt.Errorf("replay: allocate local port: %w", err)
	}
	fwd := e.kubectlCmd(ctx, "port-forward", "pod/"+e.podName, fmt.Sprintf("%d:8080", localPort))
	if err := fwd.Start(); err != nil {
		return Endpoint{}, fmt.Errorf("replay: kubectl port-forward: %w", err)
	}
	// Note: the port-forward subprocess is intentionally not tracked for
	// cleanup beyond Teardown deleting the pod (which also ends the
	// forward); see Teardown.

	return Endpoint{BaseURL: fmt.Sprintf("http://127.0.0.1:%d", localPort)}, nil
}

// Reset is a no-op; Kubernetes-based replay is expected to use a fresh
// pod per run (via Teardown + Provision) rather than resetting in place.
func (e *KubernetesEnvironment) Reset(ctx context.Context) error { return nil }

// Teardown deletes the pod.
func (e *KubernetesEnvironment) Teardown(ctx context.Context) error {
	if e.podName == "" {
		return nil
	}
	out, err := e.kubectlCmd(ctx, "delete", "pod", e.podName, "--ignore-not-found", "--wait=false").CombinedOutput()
	if err != nil {
		return fmt.Errorf("replay: kubectl delete pod %s: %w: %s", e.podName, err, out)
	}
	return nil
}

func sanitizePodName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		default:
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "build"
	}
	return string(out)
}
