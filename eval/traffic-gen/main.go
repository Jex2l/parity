// Command traffic-gen sends synthetic, Zipfian-weighted traffic (with a
// small fraction of edge cases and error requests) against a target URL —
// normally the capture proxy in front of the demo service.
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"
)

type endpoint struct {
	path   string
	weight int
}

func main() {
	target := flag.String("target", "http://localhost:8080", "base URL to send traffic to")
	n := flag.Int("n", 2000, "number of requests to send")
	concurrency := flag.Int("concurrency", 16, "concurrent workers")
	flag.Parse()

	endpoints := []endpoint{
		{"/users?page=1&page_size=10", 30},
		{"/users?page=2&page_size=10", 10},
		{"/users/5", 20},
		{"/users/9999", 2}, // 404 edge case
		{"/orders?status=paid", 15},
		{"/orders/3", 15},
		{"/orders/99999", 2}, // 404 edge case
		{"/stats/revenue", 8},
		{"/slow", 3},
		{"/health", 5},
	}
	total := 0
	for _, e := range endpoints {
		total += e.weight
	}

	jobs := make(chan string, *concurrency)
	var wg sync.WaitGroup
	client := &http.Client{Timeout: 5 * time.Second}
	errs := 0
	var mu sync.Mutex

	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				resp, err := client.Get(*target + path)
				if err != nil {
					mu.Lock()
					errs++
					mu.Unlock()
					continue
				}
				resp.Body.Close()
			}
		}()
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < *n; i++ {
		r := rng.Intn(total)
		acc := 0
		for _, e := range endpoints {
			acc += e.weight
			if r < acc {
				jobs <- e.path
				break
			}
		}
	}
	close(jobs)
	wg.Wait()

	fmt.Fprintf(os.Stdout, "sent %d requests to %s, %d transport errors\n", *n, *target, errs)
	if errs > *n/10 {
		log.Fatalf("too many transport errors (%d/%d) — is the target reachable?", errs, *n)
	}
}
