package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type metrics struct {
	mu       sync.Mutex
	requests map[string]uint64
	buckets  map[string][]uint64
}

var (
	service = getenv("SERVICE", "checkout")
	ns      = getenv("NAMESPACE", "shop")
	mode    = getenv("MODE", "service")
	fail    atomic.Bool
	stats   = metrics{requests: map[string]uint64{}, buckets: map[string][]uint64{}}
	bounds  = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5}
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		resp, err := http.Get("http://127.0.0.1:" + getenv("PORT", "8080") + "/health")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		_ = resp.Body.Close()
		return
	}
	if mode == "loadgen" {
		loadgen()
		return
	}
	h := http.NewServeMux()
	h.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h.HandleFunc("/metrics", metricsHandler)
	h.HandleFunc("/admin/fail", failureHandler)
	h.HandleFunc("/checkout", checkoutHandler)
	h.HandleFunc("/payment", paymentHandler)
	addr := ":" + getenv("PORT", "8080")
	log.Printf("service=%s namespace=%s listening=%s", service, ns, addr)
	if err := http.ListenAndServe(addr, h); err != nil {
		log.Fatal(err)
	}
}

func checkoutHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	code := http.StatusOK
	defer func() { observe(code, time.Since(start)) }()

	client := &http.Client{Timeout: 2 * time.Second}
	paymentURL := strings.TrimRight(getenv("DOWNSTREAM_URL", "http://payments:8080"), "/") + "/payment"
	resp, err := client.Get(paymentURL)
	if err != nil || resp.StatusCode >= 500 {
		if resp != nil {
			_ = resp.Body.Close()
		}
		code = http.StatusBadGateway
		message := fmt.Sprintf("checkout downstream failure: %v", err)
		if resp != nil {
			message = fmt.Sprintf("checkout downstream failure: HTTP %d", resp.StatusCode)
		}
		log.Printf("ERROR %s", message)
		pushLog(message)
		http.Error(w, message, code)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	fmt.Fprintln(w, "checkout ok")
}

func paymentHandler(w http.ResponseWriter, _ *http.Request) {
	start := time.Now()
	code := http.StatusOK
	defer func() { observe(code, time.Since(start)) }()
	if fail.Load() {
		code = http.StatusInternalServerError
		message := "payments database timeout"
		log.Printf("ERROR %s", message)
		pushLog(message)
		http.Error(w, message, code)
		return
	}
	time.Sleep(20 * time.Millisecond)
	fmt.Fprintln(w, "payment ok")
}

func failureHandler(w http.ResponseWriter, r *http.Request) {
	enabled := r.URL.Query().Get("enabled") != "false"
	fail.Store(enabled)
	log.Printf("failure mode enabled=%t", enabled)
	fmt.Fprintf(w, "failure=%t\n", enabled)
}

func observe(code int, elapsed time.Duration) {
	key := strconv.Itoa(code)
	seconds := elapsed.Seconds()
	stats.mu.Lock()
	stats.requests[key]++
	counts := stats.buckets[key]
	if counts == nil {
		counts = make([]uint64, len(bounds)+1)
	}
	for i, bound := range bounds {
		if seconds <= bound {
			counts[i]++
		}
	}
	counts[len(bounds)]++
	stats.buckets[key] = counts
	stats.mu.Unlock()
}

func metricsHandler(w http.ResponseWriter, _ *http.Request) {
	stats.mu.Lock()
	defer stats.mu.Unlock()
	for code, count := range stats.requests {
		fmt.Fprintf(w, "http_requests_total{namespace=%q,service=%q,code=%q} %d\n", ns, service, code, count)
		counts := stats.buckets[code]
		for i, bound := range bounds {
			fmt.Fprintf(w, "http_request_duration_seconds_bucket{namespace=%q,service=%q,le=%q} %d\n", ns, service, strconv.FormatFloat(bound, 'f', -1, 64), counts[i])
		}
		fmt.Fprintf(w, "http_request_duration_seconds_bucket{namespace=%q,service=%q,le=\"+Inf\"} %d\n", ns, service, counts[len(bounds)])
	}
}

func loadgen() {
	target := strings.TrimRight(getenv("TARGET_URL", "http://checkout:8080"), "/") + "/checkout"
	client := &http.Client{Timeout: 3 * time.Second}
	for {
		resp, err := client.Get(target)
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		if err != nil {
			log.Printf("loadgen error: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func pushLog(message string) {
	url := strings.TrimRight(os.Getenv("LOKI_URL"), "/") + "/loki/api/v1/push"
	if os.Getenv("LOKI_URL") == "" {
		return
	}
	payload := map[string]any{"streams": []any{map[string]any{
		"stream": map[string]string{"namespace": ns, "service": service, "level": "error"},
		"values": [][]string{{fmt.Sprintf("%d", time.Now().UnixNano()), message}},
	}}}
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
