package gitdiff

import (
	"strings"
	"testing"
)

func TestParse_RealGitDiffOutput_SingleFileAddedField(t *testing.T) {
	// Actual shape `git diff` produces for a Go struct-literal field
	// addition — this is (reconstructed from) the real diff behind
	// jex2l/parity-client-test#4.
	diffText := `diff --git a/cmd/widget-api/main.go b/cmd/widget-api/main.go
index abc1234..def5678 100644
--- a/cmd/widget-api/main.go
+++ b/cmd/widget-api/main.go
@@ -14,6 +14,7 @@ type widget struct {
 	ID    int    ` + "`json:\"id\"`" + `
 	Name  string ` + "`json:\"name\"`" + `
 	Price int    ` + "`json:\"price_cents\"`" + `
+	InStock bool ` + "`json:\"in_stock\"`" + `
 }
@@ -29,3 +30,3 @@ func main() {
-			{ID: 1, Name: "bolt", Price: 199},
+			{ID: 1, Name: "bolt", Price: 199, InStock: true},
`

	hunks := Parse(diffText)

	if len(hunks) != 1 {
		t.Fatalf("got %d hunks, want 1: %+v", len(hunks), hunks)
	}
	h := hunks[0]
	if h.File != "cmd/widget-api/main.go" {
		t.Fatalf("File = %q, want cmd/widget-api/main.go", h.File)
	}
	if len(h.Added) != 2 {
		t.Fatalf("Added = %v, want 2 lines", h.Added)
	}
	foundStructField := false
	foundLiteral := false
	for _, l := range h.Added {
		if strings.Contains(l, "InStock bool") {
			foundStructField = true
		}
		if strings.Contains(l, "InStock: true") {
			foundLiteral = true
		}
	}
	if !foundStructField || !foundLiteral {
		t.Fatalf("expected both the struct field and literal additions, got: %v", h.Added)
	}
	if len(h.Removed) != 1 || !strings.Contains(h.Removed[0], `Price: 199`) {
		t.Fatalf("Removed = %v, want the old struct literal line", h.Removed)
	}
}

func TestParse_MultipleFiles_SeparateHunksPerFile(t *testing.T) {
	diffText := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,1 +1,1 @@
-old a
+new a
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -1,1 +1,1 @@
-old b
+new b
`
	hunks := Parse(diffText)
	if len(hunks) != 2 {
		t.Fatalf("got %d hunks, want 2: %+v", len(hunks), hunks)
	}
	if hunks[0].File != "a.go" || hunks[1].File != "b.go" {
		t.Fatalf("unexpected file order: %+v", hunks)
	}
}

func TestParse_NewFile_DevNullBase(t *testing.T) {
	diffText := `diff --git a/new.go b/new.go
new file mode 100644
--- /dev/null
+++ b/new.go
@@ -0,0 +1,2 @@
+package main
+// new file
`
	hunks := Parse(diffText)
	if len(hunks) != 1 {
		t.Fatalf("got %d hunks, want 1: %+v", len(hunks), hunks)
	}
	if hunks[0].File != "new.go" {
		t.Fatalf("File = %q, want new.go", hunks[0].File)
	}
	if len(hunks[0].Added) != 2 {
		t.Fatalf("Added = %v, want 2 lines", hunks[0].Added)
	}
}

func TestParse_EmptyInput_NoHunks(t *testing.T) {
	if hunks := Parse(""); len(hunks) != 0 {
		t.Fatalf("got %d hunks for empty input, want 0", len(hunks))
	}
}

func TestParse_PlusPlusPlusLineNotMistakenForContent(t *testing.T) {
	// Regression guard: "+++ b/file.go" itself must never end up as an
	// "added line" — it's metadata, not content, even though it starts
	// with "+".
	diffText := `diff --git a/f.go b/f.go
--- a/f.go
+++ b/f.go
@@ -1,1 +1,1 @@
+real added line
`
	hunks := Parse(diffText)
	if len(hunks) != 1 {
		t.Fatalf("got %d hunks, want 1", len(hunks))
	}
	for _, l := range hunks[0].Added {
		if strings.Contains(l, "+++") {
			t.Fatalf("a +++ metadata line leaked into Added: %q", l)
		}
	}
	if len(hunks[0].Added) != 1 || hunks[0].Added[0] != "real added line" {
		t.Fatalf("Added = %v, want exactly [\"real added line\"]", hunks[0].Added)
	}
}
