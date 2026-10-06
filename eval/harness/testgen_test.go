// testgen_test.go proves the Phase 3 acceptance criterion: tests
// generated from recorded exchanges actually catch the mutation they were
// distilled from (fail against the mutated candidate, pass against the
// unmutated base) — not just that they execute without error.
package harness

import (
	"context"
	"testing"

	"github.com/jex2l/parity/internal/replay"
	"github.com/jex2l/parity/internal/testgen"
)

func TestGeneratedTests_CatchTheirOwnMutation(t *testing.T) {
	bin := buildDemoService(t)
	exchanges := recordBaseline(t, bin) // unmutated recordings, including /orders/{id}

	cases := testgen.Generate(exchanges, nil)
	if len(cases) == 0 {
		t.Fatal("expected at least one generated test case")
	}

	runAgainst := func(mutationEnv string) []testgen.Result {
		env := replay.NewProcessEnvironment("DEMO_ADDR", "/health")
		buildEnv := map[string]string{}
		if mutationEnv != "" {
			buildEnv["PARITY_MUTATION"] = mutationEnv
		}
		ep, err := env.Provision(context.Background(), replay.BuildRef{Name: "test-run", Cmd: bin, Env: buildEnv})
		if err != nil {
			t.Fatalf("provision (%s): %v", mutationEnv, err)
		}
		defer env.Teardown(context.Background())
		return testgen.Run(ep.BaseURL, cases)
	}

	t.Run("passes_against_unmutated_base", func(t *testing.T) {
		results := runAgainst("")
		for _, r := range results {
			if !r.Passed {
				t.Errorf("case %s failed against the unmutated base it was distilled from: %s", r.Case.Name, r.Reason)
			}
		}
	})

	t.Run("fails_against_field_rename_mutation", func(t *testing.T) {
		results := runAgainst("field_rename")
		anyFailed := false
		for _, r := range results {
			if r.Case.Path != "" && r.Case.ExpectedStatus == 200 {
				// Only /orders/{id} 200-status cases are affected by this mutation.
				if !r.Passed {
					anyFailed = true
				}
			}
		}
		if !anyFailed {
			t.Fatalf("expected at least one generated test to fail against the field_rename mutation (status/field_added+removed would otherwise go undetected)")
		}
	})

	t.Run("fails_against_status_5xx_mutation", func(t *testing.T) {
		results := runAgainst("status_5xx")
		anyFailed := false
		for _, r := range results {
			if !r.Passed {
				anyFailed = true
			}
		}
		if !anyFailed {
			t.Fatalf("expected at least one generated test to fail against the status_5xx mutation")
		}
	})
}
