// grpc_test.go proves Phase 5's gRPC diffing acceptance criterion: a
// value change detected through a real round trip against an actual
// running gRPC server (two real process instances, real network calls),
// not hand-constructed messages compared offline.
package harness

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/jex2l/parity/eval/grpc-demo/pb"
	"github.com/jex2l/parity/internal/grpcdiff"
)

func buildGRPCDemo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "grpc-demo")
	cmd := exec.Command("go", "build", "-o", bin, "../grpc-demo")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building grpc-demo: %v\n%s", err, out)
	}
	return bin
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func startGRPCInstance(t *testing.T, bin string, mutationEnv string) string {
	t.Helper()
	port := freeTCPPort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), fmt.Sprintf("GRPC_ADDR=:%d", port))
	if mutationEnv != "" {
		cmd.Env = append(cmd.Env, "PARITY_MUTATION="+mutationEnv)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start grpc-demo: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return addr
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("grpc-demo on %s never started listening", addr)
	return ""
}

func TestGRPCDiff_DetectsRealMutationThroughLiveRoundTrip(t *testing.T) {
	bin := buildGRPCDemo(t)

	baseAddr := startGRPCInstance(t, bin, "")
	mutatedAddr := startGRPCInstance(t, bin, "total_changed")

	baseConn, err := grpc.NewClient(baseAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial base: %v", err)
	}
	defer baseConn.Close()
	mutatedConn, err := grpc.NewClient(mutatedAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial mutated: %v", err)
	}
	defer mutatedConn.Close()

	baseClient := pb.NewOrdersServiceClient(baseConn)
	mutatedClient := pb.NewOrdersServiceClient(mutatedConn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	baseOrder, err := baseClient.GetOrder(ctx, &pb.GetOrderRequest{Id: 7})
	if err != nil {
		t.Fatalf("base GetOrder: %v", err)
	}
	mutatedOrder, err := mutatedClient.GetOrder(ctx, &pb.GetOrderRequest{Id: 7})
	if err != nil {
		t.Fatalf("mutated GetOrder: %v", err)
	}

	changes := grpcdiff.Compare(baseOrder, mutatedOrder)
	if len(changes) != 1 || changes[0].Path != "$.total" {
		t.Fatalf("expected exactly one VALUE_CHANGED at $.total from a real round trip, got %+v (base=%v mutated=%v)", changes, baseOrder, mutatedOrder)
	}
	t.Logf("detected real gRPC diff: %+v", changes[0])
}
