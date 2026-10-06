package grpcdiff

import (
	"testing"

	"github.com/jex2l/parity/internal/diff"
	pb "github.com/jex2l/parity/eval/grpc-demo/pb"
)

func TestCompare_IdenticalIsEmpty(t *testing.T) {
	a := &pb.Order{Id: 1, UserId: 2, Total: 9.5, Status: "paid"}
	b := &pb.Order{Id: 1, UserId: 2, Total: 9.5, Status: "paid"}
	changes := Compare(a, b)
	if len(changes) != 0 {
		t.Fatalf("expected no changes for identical messages, got %+v", changes)
	}
}

func TestCompare_DetectsScalarValueChange(t *testing.T) {
	a := &pb.Order{Id: 1, Total: 9.5, Status: "paid"}
	b := &pb.Order{Id: 1, Total: 12.0, Status: "paid"}
	changes := Compare(a, b)
	if len(changes) != 1 || changes[0].Path != "$.total" || changes[0].Type != diff.ValueChanged {
		t.Fatalf("expected one VALUE_CHANGED at $.total, got %+v", changes)
	}
}

func TestCompare_DetectsStatusClearedToZeroValue(t *testing.T) {
	a := &pb.Order{Id: 1, Status: "paid"}
	b := &pb.Order{Id: 1, Status: ""}
	changes := Compare(a, b)
	found := false
	for _, c := range changes {
		if c.Path == "$.status" && c.Type == diff.ValueChanged {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a VALUE_CHANGED at $.status when it's cleared, got %+v", changes)
	}
}
