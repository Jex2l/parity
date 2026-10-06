// Command grpc-demo is a minimal gRPC service used only to prove Phase
// 5's protobuf-aware structural diffing against a real round trip. It
// mirrors eval/demo-service's PARITY_MUTATION convenience (see
// docs/plans/phase-2.md for why that's a harness-only convention).
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"

	"google.golang.org/grpc"

	pb "github.com/jex2l/parity/eval/grpc-demo/pb"
)

var mutation = os.Getenv("PARITY_MUTATION")

type server struct {
	pb.UnimplementedOrdersServiceServer
}

func (server) GetOrder(ctx context.Context, req *pb.GetOrderRequest) (*pb.Order, error) {
	o := &pb.Order{Id: req.Id, UserId: req.Id % 50, Total: float64(req.Id) + 0.5, Status: "paid"}
	switch mutation {
	case "status_renamed":
		// Unintended: no direct proto equivalent of a JSON field rename,
		// but changing which field carries the value (here, repurposing
		// Status to hold something else) is the protobuf analogue.
		o.Status = ""
	case "total_changed":
		o.Total = float64(req.Id) // unintended: dropped the +0.5
	}
	return o, nil
}

func main() {
	addr := ":9090"
	if v := os.Getenv("GRPC_ADDR"); v != "" {
		addr = v
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	s := grpc.NewServer()
	pb.RegisterOrdersServiceServer(s, server{})
	fmt.Printf("grpc-demo listening on %s\n", addr)
	log.Fatal(s.Serve(lis))
}
