package feedback

import (
	"path/filepath"
	"testing"
)

func TestStore_RecordAndQuery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acks.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	ok, err := s.IsAcknowledgedIntended("cluster-a")
	if err != nil || ok {
		t.Fatalf("expected unknown cluster to be unacknowledged, got ok=%v err=%v", ok, err)
	}

	if err := s.Record("cluster-a", VerdictAcknowledgedIntended, "new field, expected"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	ok, err = s.IsAcknowledgedIntended("cluster-a")
	if err != nil || !ok {
		t.Fatalf("expected cluster-a to be acknowledged intended, got ok=%v err=%v", ok, err)
	}

	if err := s.Record("cluster-b", VerdictBug, "actual regression"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	ok, _ = s.IsAcknowledgedIntended("cluster-b")
	if ok {
		t.Fatalf("cluster-b was marked a bug, not intended")
	}

	// Re-opening the store must see persisted data.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	all, err := s2.All()
	if err != nil || len(all) != 2 {
		t.Fatalf("expected 2 persisted entries, got %d (err=%v)", len(all), err)
	}
}

func TestStore_RecordReplacesExistingEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acks.json")
	s, _ := Open(path)
	s.Record("c1", VerdictBug, "first verdict")
	s.Record("c1", VerdictAcknowledgedIntended, "actually fine")

	all, _ := s.All()
	if len(all) != 1 {
		t.Fatalf("expected re-recording the same cluster to replace, not append, got %d entries", len(all))
	}
	if all[0].Verdict != VerdictAcknowledgedIntended {
		t.Fatalf("expected updated verdict, got %v", all[0].Verdict)
	}
}
