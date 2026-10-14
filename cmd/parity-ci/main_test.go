package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jex2l/parity/internal/classify"
	"github.com/jex2l/parity/internal/diff"
	"github.com/jex2l/parity/internal/gh"
	"github.com/jex2l/parity/internal/report"
)

func writeReport(t *testing.T, dir string, rep report.Report) string {
	t.Helper()
	path := filepath.Join(dir, "report.json")
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	return path
}

func cleanReport() report.Report {
	return report.Report{RunID: "run-1", TotalReplayed: 10, Fidelity: report.FidelitySummary{Score: 1.0}}
}

// dirtyReport's cluster has ChangeTypes "modified" (not FIELD_ADDED,
// FIELD_REMOVED, TYPE_CHANGED, or ERROR_APPEARED) and Severity "critical"
// without an ERROR_APPEARED change type, so none of classify.Heuristic's
// rules 1-4 match and it falls through to the default Uncertain verdict
// — i.e. this report's cluster is classified Uncertain, not
// LikelyUnintended, despite the "critical" severity label (critical
// severity alone isn't rule 1's trigger; an actual 5xx appearing is).
func dirtyReport() report.Report {
	return report.Report{
		RunID:         "run-2",
		TotalReplayed: 10,
		Fidelity:      report.FidelitySummary{Score: 0.9},
		SignalCount:   1,
		Clusters: []report.ClusterView{
			{Endpoint: "/users/{id}", ChangedPaths: []string{"email"}, ChangeTypes: []string{"modified"}, Severity: "critical", Count: 1, WeightedCount: 1},
		},
	}
}

// likelyIntendedReport mirrors the real scenario from validating this
// tool end-to-end on jex2l/parity-client-test#2: a low-severity,
// additive-only field change, which classify.Heuristic's rule 3 labels
// LikelyIntended.
func likelyIntendedReport() report.Report {
	return report.Report{
		RunID:         "run-3",
		TotalReplayed: 500,
		Fidelity:      report.FidelitySummary{Score: 1.0},
		SignalCount:   1,
		Clusters: []report.ClusterView{
			{Endpoint: "/widgets", ChangedPaths: []string{"$.sku"}, ChangeTypes: []string{string(diff.FieldAdded)}, Severity: "low", Count: 500, WeightedCount: 500},
		},
	}
}

func TestDryRun_CleanReport_ExitsCleanAndPrintsSummary(t *testing.T) {
	dir := t.TempDir()
	path := writeReport(t, dir, cleanReport())

	stdout, stderr := captureOutput(t, dir)
	code := run([]string{"--report", path, "--dry-run"}, stdout.file, stderr.file)

	if code != exitClean {
		t.Fatalf("exit code = %d, want %d", code, exitClean)
	}
	out := stdout.read(t)
	if !strings.Contains(out, "No behavioral differences") {
		t.Fatalf("expected clean-report markdown, got: %s", out)
	}
}

func TestDryRun_ClustersFound_ExitsUnintended(t *testing.T) {
	dir := t.TempDir()
	path := writeReport(t, dir, dirtyReport())

	stdout, stderr := captureOutput(t, dir)
	code := run([]string{"--report", path, "--dry-run"}, stdout.file, stderr.file)

	if code != exitUnintended {
		t.Fatalf("exit code = %d, want %d", code, exitUnintended)
	}
	out := stdout.read(t)
	if !strings.Contains(out, "/users/{id}") {
		t.Fatalf("expected cluster endpoint in markdown, got: %s", out)
	}
}

func TestDryRun_AllClustersLikelyIntended_DoesNotFailTheCheck(t *testing.T) {
	// Regression test for the real bug found validating this tool
	// end-to-end: every single test PR failed the check, including one
	// the classifier itself labeled LIKELY_INTENDED — because outcome()
	// used to only check len(rep.Clusters), ignoring the classification
	// entirely. A confidently-intended, additive-only change must not
	// produce exitUnintended/ConclusionFailure.
	dir := t.TempDir()
	path := writeReport(t, dir, likelyIntendedReport())

	stdout, stderr := captureOutput(t, dir)
	code := run([]string{"--report", path, "--dry-run"}, stdout.file, stderr.file)

	if code != exitLikelyIntended {
		t.Fatalf("exit code = %d, want %d (should NOT be exitUnintended=%d)", code, exitLikelyIntended, exitUnintended)
	}
	out := stdout.read(t)
	if !strings.Contains(out, "LIKELY_INTENDED") {
		t.Fatalf("expected LIKELY_INTENDED in markdown, got: %s", out)
	}
}

func TestDryRun_LowConfidence_ExitsFidelityLow(t *testing.T) {
	dir := t.TempDir()
	rep := cleanReport()
	rep.Fidelity.LowConfidence = true
	rep.Fidelity.Reason = "too few samples"
	path := writeReport(t, dir, rep)

	stdout, stderr := captureOutput(t, dir)
	code := run([]string{"--report", path, "--dry-run"}, stdout.file, stderr.file)

	if code != exitFidelityLow {
		t.Fatalf("exit code = %d, want %d", code, exitFidelityLow)
	}
}

func TestMissingReportFile_ExitsInfraError(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr := captureOutput(t, dir)
	code := run([]string{"--report", filepath.Join(dir, "nope.json"), "--dry-run"}, stdout.file, stderr.file)
	if code != exitInfraError {
		t.Fatalf("exit code = %d, want %d", code, exitInfraError)
	}
}

func TestMissingOwnerRepoPR_ExitsInfraErrorBeforeAnyHTTPCall(t *testing.T) {
	dir := t.TempDir()
	path := writeReport(t, dir, dirtyReport())
	t.Setenv("PARITY_TEST_TOKEN", "test-token")
	t.Setenv("GITHUB_REPOSITORY", "")

	stdout, stderr := captureOutput(t, dir)
	code := run([]string{"--report", path, "--token-env", "PARITY_TEST_TOKEN"}, stdout.file, stderr.file)
	if code != exitInfraError {
		t.Fatalf("exit code = %d, want %d", code, exitInfraError)
	}
	if !strings.Contains(stderr.read(t), "--owner, --repo and --pr are required") {
		t.Fatalf("expected owner/repo/pr guard message, got: %s", stderr.read(t))
	}
}

func TestPostsCheckRunAndComment_AgainstFakeGitHub(t *testing.T) {
	var gotCheckRun gh.CheckRun
	var gotCommentBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/issues/"):
			json.NewEncoder(w).Encode([]gh.Comment{})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/issues/"):
			var c gh.Comment
			json.NewDecoder(r.Body).Decode(&c)
			gotCommentBody = c.Body
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(gh.Comment{ID: 1})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/check-runs"):
			json.NewDecoder(r.Body).Decode(&gotCheckRun)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"id": 1})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := writeReport(t, dir, dirtyReport())

	t.Setenv("PARITY_TEST_TOKEN", "test-token")
	t.Setenv("GITHUB_REPOSITORY", "")
	t.Setenv("GITHUB_SHA", "")

	stdout, stderr := captureOutput(t, dir)
	code := run([]string{
		"--report", path,
		"--token-env", "PARITY_TEST_TOKEN",
		"--owner", "acme", "--repo", "widgets", "--pr", "42", "--sha", "abc123",
		"--api-base-url", srv.URL,
	}, stdout.file, stderr.file)

	if code != exitUnintended {
		t.Fatalf("exit code = %d, want %d; stderr=%s", code, exitUnintended, stderr.read(t))
	}
	if gotCommentBody == "" {
		t.Fatalf("expected a PR comment to be posted")
	}
	if !strings.Contains(gotCommentBody, "/users/{id}") {
		t.Fatalf("comment body missing cluster endpoint: %s", gotCommentBody)
	}
	if gotCheckRun.HeadSHA != "abc123" {
		t.Fatalf("check run HeadSHA = %q, want abc123", gotCheckRun.HeadSHA)
	}
	if gotCheckRun.Conclusion != gh.ConclusionFailure {
		t.Fatalf("check run conclusion = %q, want failure", gotCheckRun.Conclusion)
	}
	out := stdout.read(t)
	if !strings.Contains(out, "acme/widgets#42") {
		t.Fatalf("expected posting confirmation on stdout, got: %s", out)
	}
}

func TestOutcomeAndCheckTitle(t *testing.T) {
	noPR := classify.PRContext{}

	conclusion, code := outcome(cleanReport(), nil)
	if conclusion != gh.ConclusionSuccess || code != exitClean {
		t.Fatalf("clean report: got (%s, %d)", conclusion, code)
	}

	dirty := dirtyReport()
	conclusion, code = outcome(dirty, classifyClusters(dirty, noPR))
	if conclusion != gh.ConclusionFailure || code != exitUnintended {
		t.Fatalf("dirty report: got (%s, %d)", conclusion, code)
	}

	intended := likelyIntendedReport()
	conclusion, code = outcome(intended, classifyClusters(intended, noPR))
	if conclusion != gh.ConclusionNeutral || code != exitLikelyIntended {
		t.Fatalf("all-likely-intended report: got (%s, %d), want (neutral, %d)", conclusion, code, exitLikelyIntended)
	}

	lowConf := cleanReport()
	lowConf.Fidelity.LowConfidence = true
	conclusion, code = outcome(lowConf, nil)
	if conclusion != gh.ConclusionNeutral || code != exitFidelityLow {
		t.Fatalf("low-confidence report: got (%s, %d)", conclusion, code)
	}

	if title := checkTitle(dirty, classifyClusters(dirty, noPR)); !strings.Contains(title, "need review") {
		t.Fatalf("unexpected check title for an uncertain cluster: %q", title)
	}
	if title := checkTitle(intended, classifyClusters(intended, noPR)); !strings.Contains(title, "all look intended") {
		t.Fatalf("unexpected check title for an all-intended report: %q", title)
	}
}

func TestOutcome_MixedClusters_OneUncertainStillFailsTheCheck(t *testing.T) {
	// Even if some clusters are confidently intended, a single
	// UNCERTAIN or LIKELY_UNINTENDED cluster must still fail the check
	// — "mostly fine" isn't the bar, "nothing needs a human look" is.
	mixed := report.Report{
		Clusters: []report.ClusterView{
			{Endpoint: "/widgets", ChangedPaths: []string{"$.sku"}, ChangeTypes: []string{string(diff.FieldAdded)}, Severity: "low"},
			{Endpoint: "/widgets", ChangedPaths: []string{"$.price_cents"}, ChangeTypes: []string{"modified"}, Severity: "medium"},
		},
	}
	conclusion, code := outcome(mixed, classifyClusters(mixed, classify.PRContext{}))
	if conclusion != gh.ConclusionFailure || code != exitUnintended {
		t.Fatalf("mixed report: got (%s, %d), want (failure, %d)", conclusion, code, exitUnintended)
	}
}

func TestParseFlags_ChangedFilesSplit(t *testing.T) {
	cfg, err := parseFlags([]string{"--report", "r.json", "--changed-files", "a.go,b.go"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if len(cfg.changedFiles) != 2 || cfg.changedFiles[0] != "a.go" || cfg.changedFiles[1] != "b.go" {
		t.Fatalf("unexpected changedFiles: %v", cfg.changedFiles)
	}
}

func TestParseFlags_MissingReport_Errors(t *testing.T) {
	if _, err := parseFlags([]string{"--dry-run"}); err == nil {
		t.Fatalf("expected an error when --report is missing")
	}
}

// --- test helpers ---

type capturedFile struct {
	file *os.File
	path string
}

func captureOutput(t *testing.T, dir string) (stdout, stderr capturedFile) {
	t.Helper()
	outPath := filepath.Join(dir, "stdout.txt")
	errPath := filepath.Join(dir, "stderr.txt")
	outF, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("create stdout capture: %v", err)
	}
	errF, err := os.Create(errPath)
	if err != nil {
		t.Fatalf("create stderr capture: %v", err)
	}
	t.Cleanup(func() { outF.Close(); errF.Close() })
	return capturedFile{outF, outPath}, capturedFile{errF, errPath}
}

func (c capturedFile) read(t *testing.T) string {
	t.Helper()
	c.file.Sync()
	b, err := os.ReadFile(c.path)
	if err != nil {
		t.Fatalf("read captured output: %v", err)
	}
	return string(b)
}
