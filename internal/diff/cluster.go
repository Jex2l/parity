package diff

import (
	"fmt"
	"sort"
	"strings"
)

// ExchangeDiff is one replayed exchange's diff result plus enough identity
// to build a report (the exchange ID and endpoint template it came from).
type ExchangeDiff struct {
	ExchangeID string
	Endpoint   string
	Method     string
	Path       string // concrete request path, e.g. "/orders/42" (PathTemplate is Endpoint)
	Signal     []Change
	Absorbed   []Change
	Weight     float64 // sampling weight, for blast-radius estimates
	// BeforeBody/AfterBody are the full response bodies this diff's
	// Changes were computed from — kept so a report/UI can render a
	// real before/after example, not just the structural diff summary.
	// Already scrubbed if the caller configured a Scrubber (see
	// internal/orchestrate.Config.Scrubber).
	BeforeBody []byte
	AfterBody  []byte
}

// Example is one concrete exchange backing a Cluster, carrying enough to
// render a real before/after comparison (the UI's "example browser").
type Example struct {
	ExchangeID string
	Method     string
	Path       string
	Changes    []Change
	BeforeBody string
	AfterBody  string
}

// Cluster groups exchanges whose signal changes share the same endpoint,
// set of changed paths, and set of change types — per spec 6.5(6): report
// "3 clusters", not "212 diffs".
type Cluster struct {
	Key              string
	Endpoint         string
	ChangedPaths     []string
	ChangeTypes      []string
	Severity         Severity
	Count            int
	WeightedCount    float64
	ExampleExchangeIDs []string // a few representative/diverse examples
	Examples         []Example  // same examples, with full before/after + per-change detail
}

const maxExamplesPerCluster = 5

// BuildClusters groups a set of per-exchange diffs (already filtered to
// their Signal changes) into clusters.
func BuildClusters(diffs []ExchangeDiff) []Cluster {
	return buildClustersFrom(diffs, func(d ExchangeDiff) []Change { return d.Signal })
}

// BuildAbsorbedClusters groups the same per-exchange diffs by their
// Absorbed changes instead of Signal — the "what the noise model
// filtered out, and why" view a noise-model inspector needs. A change
// absorbed as noise still gets grouped and shown, just separately from
// the clusters that represent real signal.
func BuildAbsorbedClusters(diffs []ExchangeDiff) []Cluster {
	return buildClustersFrom(diffs, func(d ExchangeDiff) []Change { return d.Absorbed })
}

func buildClustersFrom(diffs []ExchangeDiff, changesOf func(ExchangeDiff) []Change) []Cluster {
	byKey := map[string]*Cluster{}
	order := []string{}

	for _, d := range diffs {
		changes := changesOf(d)
		if len(changes) == 0 {
			continue
		}
		key := clusterKey(d.Endpoint, changes)
		c, ok := byKey[key]
		if !ok {
			paths, types := pathsAndTypes(changes)
			c = &Cluster{
				Key:          key,
				Endpoint:     d.Endpoint,
				ChangedPaths: paths,
				ChangeTypes:  types,
			}
			byKey[key] = c
			order = append(order, key)
		}
		c.Count++
		if d.Weight > 0 {
			c.WeightedCount += d.Weight
		} else {
			c.WeightedCount++
		}
		if sev := MaxSeverity(changes); sev > c.Severity {
			c.Severity = sev
		}
		if len(c.ExampleExchangeIDs) < maxExamplesPerCluster {
			c.ExampleExchangeIDs = append(c.ExampleExchangeIDs, d.ExchangeID)
			c.Examples = append(c.Examples, Example{
				ExchangeID: d.ExchangeID,
				Method:     d.Method,
				Path:       d.Path,
				Changes:    changes,
				BeforeBody: string(d.BeforeBody),
				AfterBody:  string(d.AfterBody),
			})
		}
	}

	out := make([]Cluster, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	// Most severe, then largest blast radius, first — reviewers see
	// unintended-looking, high-impact clusters before cosmetic ones.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity > out[j].Severity
		}
		return out[i].WeightedCount > out[j].WeightedCount
	})
	return out
}

func clusterKey(endpoint string, changes []Change) string {
	paths, types := pathsAndTypes(changes)
	return fmt.Sprintf("%s|%s|%s", endpoint, strings.Join(paths, ","), strings.Join(types, ","))
}

func pathsAndTypes(changes []Change) (paths, types []string) {
	pathSet := map[string]bool{}
	typeSet := map[string]bool{}
	for _, c := range changes {
		pathSet[c.Path] = true
		typeSet[string(c.Type)] = true
	}
	for p := range pathSet {
		paths = append(paths, p)
	}
	for t := range typeSet {
		types = append(types, t)
	}
	sort.Strings(paths)
	sort.Strings(types)
	return paths, types
}
