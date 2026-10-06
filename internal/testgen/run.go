package testgen

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// Result is the outcome of running one Case against a live endpoint.
type Result struct {
	Case    Case
	Passed  bool
	Reason  string // non-empty only when Passed is false
}

// Run executes cases against baseURL and returns one Result per case, in
// order. It compares status codes exactly and bodies after masking both
// sides' volatile values identically, so a legitimately-different UUID or
// timestamp never fails a test that would otherwise match structurally.
func Run(baseURL string, cases []Case) []Result {
	client := &http.Client{Timeout: 10 * time.Second}
	results := make([]Result, len(cases))
	for i, c := range cases {
		results[i] = runOne(client, baseURL, c)
	}
	return results
}

func runOne(client *http.Client, baseURL string, c Case) Result {
	url := baseURL + c.Path
	if c.Query != "" {
		url += "?" + c.Query
	}
	req, err := http.NewRequest(c.Method, url, nil)
	if err != nil {
		return Result{Case: c, Passed: false, Reason: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{Case: c, Passed: false, Reason: err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != c.ExpectedStatus {
		return Result{Case: c, Passed: false, Reason: fmt.Sprintf("status = %d, want %d", resp.StatusCode, c.ExpectedStatus)}
	}
	actualMasked := MaskVolatile(string(body))
	if actualMasked != c.ExpectedBody {
		return Result{Case: c, Passed: false, Reason: fmt.Sprintf("body mismatch:\n  got:  %s\n  want: %s", actualMasked, c.ExpectedBody)}
	}
	return Result{Case: c, Passed: true}
}
