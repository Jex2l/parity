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
	Signal     []Change
	Absorbed   []Change
	Weight     float64 // sampling weight, for blast-radius estimates
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
}

const maxExamplesPerCluster = 5

// BuildClusters groups a set of per-exchange diffs (already filtered to
// their Signal changes) into clusters.
func BuildClusters(diffs []ExchangeDiff) []Cluster {
	byKey := map[string]*Cluster{}
	order := []string{}

	for _, d := range diffs {
		if len(d.Signal) == 0 {
			continue
		}
		key := clusterKey(d.Endpoint, d.Signal)
		c, ok := byKey[key]
		if !ok {
			paths, types := pathsAndTypes(d.Signal)
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
		if sev := MaxSeverity(d.Signal); sev > c.Severity {
			c.Severity = sev
		}
		if len(c.ExampleExchangeIDs) < maxExamplesPerCluster {
			c.ExampleExchangeIDs = append(c.ExampleExchangeIDs, d.ExchangeID)
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
