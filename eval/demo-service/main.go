// Command demo-service is the Parity eval demo: a small JSON API used to
// exercise capture, replay, and the diff engine in later phases. Phase 1
// uses it only to generate realistic traffic through the capture proxy.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type user struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type order struct {
	ID        int     `json:"id"`
	UserID    int     `json:"user_id"`
	Total     float64 `json:"total"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
}

var users = seedUsers(50)
var orders = seedOrders(200)

func seedUsers(n int) []user {
	out := make([]user, n)
	for i := range out {
		out[i] = user{ID: i + 1, Name: fmt.Sprintf("User %d", i+1), Email: fmt.Sprintf("user%d@example.com", i+1)}
	}
	return out
}

func seedOrders(n int) []order {
	statuses := []string{"pending", "paid", "shipped", "cancelled"}
	out := make([]order, n)
	for i := range out {
		out[i] = order{
			ID:        i + 1,
			UserID:    (i % 50) + 1,
			Total:     float64(rand.Intn(10000)) / 100,
			Status:    statuses[i%len(statuses)],
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Hour).UTC().Format(time.RFC3339),
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func main() {
	addr := ":3000"
	if v := os.Getenv("DEMO_ADDR"); v != "" {
		addr = v
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// GET /users?page=1&page_size=10  (list + pagination)
	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		if size < 1 {
			size = 10
		}
		start := (page - 1) * size
		if start > len(users) {
			start = len(users)
		}
		end := start + size
		if end > len(users) {
			end = len(users)
		}
		writeJSON(w, 200, map[string]any{"page": page, "page_size": size, "total": len(users), "items": users[start:end]})
	})

	// GET /users/{id}
	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		idStr := strings.TrimPrefix(r.URL.Path, "/users/")
		if strings.Contains(idStr, "/") {
			http.NotFound(w, r)
			return
		}
		id, err := strconv.Atoi(idStr)
		if err != nil || id < 1 || id > len(users) {
			writeJSON(w, 404, map[string]string{"error": "user not found"})
			return
		}
		writeJSON(w, 200, users[id-1])
	})

	// GET /orders?user_id=&status=
	mux.HandleFunc("/orders", func(w http.ResponseWriter, r *http.Request) {
		userID := r.URL.Query().Get("user_id")
		status := r.URL.Query().Get("status")
		var out []order
		for _, o := range orders {
			if userID != "" && strconv.Itoa(o.UserID) != userID {
				continue
			}
			if status != "" && o.Status != status {
				continue
			}
			out = append(out, o)
		}
		writeJSON(w, 200, map[string]any{"items": out, "total": len(out)})
	})

	// GET /orders/{id}
	mux.HandleFunc("/orders/", func(w http.ResponseWriter, r *http.Request) {
		idStr := strings.TrimPrefix(r.URL.Path, "/orders/")
		id, err := strconv.Atoi(idStr)
		if err != nil || id < 1 || id > len(orders) {
			writeJSON(w, 404, map[string]string{"error": "order not found"})
			return
		}
		writeJSON(w, 200, orders[id-1])
	})

	// GET /stats/revenue — aggregation endpoint
	mux.HandleFunc("/stats/revenue", func(w http.ResponseWriter, r *http.Request) {
		total := 0.0
		for _, o := range orders {
			if o.Status == "paid" || o.Status == "shipped" {
				total += o.Total
			}
		}
		writeJSON(w, 200, map[string]any{"total_revenue": total, "order_count": len(orders), "generated_at": time.Now().UTC().Format(time.RFC3339)})
	})

	// GET /slow — deliberately slow endpoint for perf-regression testing (Phase 3)
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})

	// POST /orders — write endpoint (flagged HAS_WRITE_SIDE_EFFECTS by capture)
	mux.HandleFunc("/orders/create", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, 201, map[string]any{"id": len(orders) + 1, "status": "pending"})
	})

	log.Printf("demo-service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
