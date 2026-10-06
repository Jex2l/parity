package replay

import "testing"

// var below only proves KubernetesEnvironment satisfies the Environment
// interface at compile time; see k8s_env.go's doc comment for why
// nothing here is tested against a real cluster.
var _ Environment = &KubernetesEnvironment{}

func TestSanitizePodName(t *testing.T) {
	cases := map[string]string{
		"base":        "base",
		"Base-B":      "base-b",
		"candidate_1": "candidate-1",
		"":            "build",
	}
	for in, want := range cases {
		if got := sanitizePodName(in); got != want {
			t.Errorf("sanitizePodName(%q) = %q, want %q", in, got, want)
		}
	}
}
