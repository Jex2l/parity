// Command parity-ci is the Parity Labs CI gate: it reads a report.json
// produced by `parity replay`, classifies each cluster with the
// heuristic baseline, renders the PR-comment markdown, and posts both a
// GitHub check run and a PR comment via internal/gh. It exists so a PR
// workflow can turn `parity replay`'s machine-readable report into a
// human-visible, actionable check without every adopting repo writing
// its own glue code.
//
// It deliberately reuses existing, already-tested packages
// (internal/report, internal/classify, internal/gh) rather than
// reimplementing anything — this command is pure composition.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/jex2l/parity/internal/classify"
	"github.com/jex2l/parity/internal/gh"
	"github.com/jex2l/parity/internal/report"
)

// Exit codes mirror cmd/parity's existing contract (see that file's
// comment) so a workflow can gate on the same exit-code semantics
// whether it calls `parity replay` directly or goes through this
// posting step.
const (
	exitClean       = 0
	exitUnintended  = 10
	exitFidelityLow = 20
	exitInfraError  = 30
)

type config struct {
	reportPath   string
	owner        string
	repo         string
	prNumber     int
	sha          string
	tokenEnv     string
	checkName    string
	testsLink    string
	changedFiles []string
	prTitle      string
	prDesc       string
	dryRun       bool
	apiBaseURL   string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	cfg, err := parseFlags(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		usage(stderr)
		return exitInfraError
	}

	b, err := os.ReadFile(cfg.reportPath)
	if err != nil {
		fmt.Fprintf(stderr, "parity-ci: reading report: %v\n", err)
		return exitInfraError
	}
	var rep report.Report
	if err := json.Unmarshal(b, &rep); err != nil {
		fmt.Fprintf(stderr, "parity-ci: parsing report: %v\n", err)
		return exitInfraError
	}

	annotations := classifyClusters(rep, classify.PRContext{
		ChangedFiles: cfg.changedFiles,
		Title:        cfg.prTitle,
		Description:  cfg.prDesc,
	})

	body := report.RenderMarkdown(rep, annotations, cfg.testsLink)
	conclusion, exitCode := outcome(rep)

	if cfg.dryRun {
		fmt.Fprintln(stdout, body)
		fmt.Fprintf(stderr, "parity-ci: dry-run, conclusion=%s, exit=%d (nothing posted to GitHub)\n", conclusion, exitCode)
		return exitCode
	}

	token := os.Getenv(cfg.tokenEnv)
	if token == "" {
		fmt.Fprintf(stderr, "parity-ci: %s is empty/unset; nothing to authenticate with. Use --dry-run to render without posting.\n", cfg.tokenEnv)
		return exitInfraError
	}
	if cfg.owner == "" || cfg.repo == "" || cfg.prNumber == 0 {
		fmt.Fprintln(stderr, "parity-ci: --owner, --repo and --pr are required unless --dry-run is set")
		return exitInfraError
	}

	client := gh.New(token)
	if cfg.apiBaseURL != "" {
		client.BaseURL = cfg.apiBaseURL
	}
	ctx := context.Background()

	if err := client.UpsertComment(ctx, cfg.owner, cfg.repo, cfg.prNumber, body); err != nil {
		fmt.Fprintf(stderr, "parity-ci: posting PR comment: %v\n", err)
		return exitInfraError
	}

	if cfg.sha != "" {
		if err := client.CreateCheckRun(ctx, cfg.owner, cfg.repo, gh.CheckRun{
			Name:       cfg.checkName,
			HeadSHA:    cfg.sha,
			Status:     "completed",
			Conclusion: conclusion,
			Output:     gh.CheckRunOutput{Title: checkTitle(rep), Summary: body},
		}); err != nil {
			fmt.Fprintf(stderr, "parity-ci: creating check run: %v\n", err)
			return exitInfraError
		}
	} else {
		fmt.Fprintln(stderr, "parity-ci: --sha not set, skipping the check run (PR comment was still posted)")
	}

	fmt.Fprintf(stdout, "parity-ci: posted to %s/%s#%d, conclusion=%s\n", cfg.owner, cfg.repo, cfg.prNumber, conclusion)
	return exitCode
}

// classifyClusters runs the heuristic classifier over every cluster,
// keeping the clusters' existing severity-first ordering (diff.BuildClusters
// already sorts that way, and report.Report preserves it).
func classifyClusters(rep report.Report, pr classify.PRContext) []report.ClusterAnnotation {
	if len(rep.Clusters) == 0 {
		return nil
	}
	h := classify.Heuristic{}
	out := make([]report.ClusterAnnotation, 0, len(rep.Clusters))
	for _, c := range rep.Clusters {
		res := h.Classify(c, pr)
		out = append(out, report.ClusterAnnotation{
			Cluster:    c,
			Label:      string(res.Label),
			Confidence: res.Confidence,
			Rationale:  res.Rationale,
		})
	}
	return out
}

// outcome maps a report onto a GitHub check conclusion and this
// command's own exit code, mirroring `parity replay`'s existing
// exitClean/exitUnintended/exitFidelityLow contract so CI gating
// behaves identically regardless of which binary produced the exit code.
func outcome(rep report.Report) (gh.CheckRunConclusion, int) {
	if rep.Fidelity.LowConfidence {
		return gh.ConclusionNeutral, exitFidelityLow
	}
	if len(rep.Clusters) > 0 {
		return gh.ConclusionFailure, exitUnintended
	}
	return gh.ConclusionSuccess, exitClean
}

func checkTitle(rep report.Report) string {
	if rep.Fidelity.LowConfidence {
		return "Parity: low-confidence replay"
	}
	if n := len(rep.Clusters); n > 0 {
		return fmt.Sprintf("Parity: %d behavioral diff cluster(s)", n)
	}
	return "Parity: no behavioral differences"
}

func parseFlags(args []string) (config, error) {
	cfg := config{
		tokenEnv:  "GITHUB_TOKEN",
		checkName: "Parity / behavioral diff",
	}
	var changedFiles string

	// Fall back to the standard GitHub Actions env vars so a workflow
	// doesn't have to pass owner/repo/sha explicitly — only --pr (the
	// event's PR number isn't in a single predictable env var across
	// push vs pull_request triggers, so it stays an explicit flag).
	if repoFull := os.Getenv("GITHUB_REPOSITORY"); repoFull != "" {
		if parts := strings.SplitN(repoFull, "/", 2); len(parts) == 2 {
			cfg.owner, cfg.repo = parts[0], parts[1]
		}
	}
	cfg.sha = os.Getenv("GITHUB_SHA")

	for i := 0; i < len(args); i++ {
		next := func() (string, error) {
			i++
			if i >= len(args) {
				return "", fmt.Errorf("parity-ci: %s requires a value", args[i-1])
			}
			return args[i], nil
		}
		var err error
		switch args[i] {
		case "--report":
			if cfg.reportPath, err = next(); err != nil {
				return cfg, err
			}
		case "--owner":
			if cfg.owner, err = next(); err != nil {
				return cfg, err
			}
		case "--repo":
			if cfg.repo, err = next(); err != nil {
				return cfg, err
			}
		case "--pr":
			var v string
			if v, err = next(); err != nil {
				return cfg, err
			}
			n, perr := strconv.Atoi(v)
			if perr != nil {
				return cfg, fmt.Errorf("parity-ci: --pr must be an integer, got %q", v)
			}
			cfg.prNumber = n
		case "--sha":
			if cfg.sha, err = next(); err != nil {
				return cfg, err
			}
		case "--token-env":
			if cfg.tokenEnv, err = next(); err != nil {
				return cfg, err
			}
		case "--check-name":
			if cfg.checkName, err = next(); err != nil {
				return cfg, err
			}
		case "--tests-link":
			if cfg.testsLink, err = next(); err != nil {
				return cfg, err
			}
		case "--changed-files":
			if changedFiles, err = next(); err != nil {
				return cfg, err
			}
		case "--pr-title":
			if cfg.prTitle, err = next(); err != nil {
				return cfg, err
			}
		case "--pr-description":
			if cfg.prDesc, err = next(); err != nil {
				return cfg, err
			}
		case "--dry-run":
			cfg.dryRun = true
		case "--api-base-url":
			if cfg.apiBaseURL, err = next(); err != nil {
				return cfg, err
			}
		default:
			return cfg, fmt.Errorf("parity-ci: unknown flag %q", args[i])
		}
	}

	if changedFiles != "" {
		cfg.changedFiles = strings.Split(changedFiles, ",")
	}
	sort.Strings(cfg.changedFiles)

	if cfg.reportPath == "" {
		return cfg, fmt.Errorf("parity-ci: --report is required")
	}
	return cfg, nil
}

func usage(w *os.File) {
	fmt.Fprintln(w, `usage:
  parity-ci --report report.json [--owner O --repo R --pr N --sha SHA] [--token-env NAME] [--check-name NAME] [--changed-files a,b,c] [--pr-title T] [--pr-description D] [--tests-link URL] [--api-base-url URL] [--dry-run]

Posts the given report.json as a PR comment and check run, classifying
each cluster with the heuristic baseline first. With --dry-run, renders
the comment body to stdout and exits without contacting GitHub — useful
for local testing or a workflow step that only wants the markdown.

Exit codes match "parity replay": 0 clean, 10 unintended diffs found,
20 low-confidence replay, 30 infra/usage error.`)
}
