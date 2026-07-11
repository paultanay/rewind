package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rewind-io/rewind/internal/model"
)

// apiResponse is the top-level Prometheus HTTP API response envelope.
type apiResponse struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	ErrorType string          `json:"errorType,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// queryRangeData holds the result of a /query_range call.
type queryRangeData struct {
	ResultType string        `json:"resultType"`
	Result     []matrixEntry `json:"result"`
}

// matrixEntry is one time series in a matrix result.
type matrixEntry struct {
	Metric map[string]string `json:"metric"`
	Values []samplePair      `json:"values"`
}

// samplePair is [timestamp_float, value_string] as returned by Prometheus.
type samplePair [2]json.RawMessage

// client is a minimal read-only Prometheus HTTP API client.
// It intentionally uses only the standard library — no Prometheus client_go
// dependency so the binary stays small and compilation stays fast.
type client struct {
	baseURL    string
	httpClient *http.Client
	headers    map[string]string
	userAgent  string
}

func newClient(baseURL string, headers map[string]string, timeout time.Duration, version string) *client {
	transport := &http.Transport{
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     30 * time.Second,
	}
	return &client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
		headers:   headers,
		userAgent: "rewind/" + version,
	}
}

// QueryRange executes a Prometheus range query and returns the matrix result.
// It retries once on transient errors (5xx, network timeout) with a 500ms back-off.
func (c *client) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]matrixEntry, error) {
	params := url.Values{
		"query": {query},
		"start": {formatTS(start)},
		"end":   {formatTS(end)},
		"step":  {strconv.Itoa(int(step.Seconds()))},
	}
	endpoint := c.baseURL + "/api/v1/query_range?" + params.Encode()

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}

		data, err := c.get(ctx, endpoint)
		if err != nil {
			lastErr = err
			continue
		}

		var qr queryRangeData
		if err := json.Unmarshal(data, &qr); err != nil {
			return nil, fmt.Errorf("prometheus: parse query_range: %w", err)
		}
		return qr.Result, nil
	}
	return nil, fmt.Errorf("prometheus: query_range after 2 attempts: %w", lastErr)
}

// Check performs a lightweight readiness probe against /-/ready.
func (c *client) Check(ctx context.Context) error {
	_, err := c.get(ctx, c.baseURL+"/-/ready")
	return err
}

func (c *client) get(ctx context.Context, endpoint string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("prometheus: build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // 32 MB guard
	if err != nil {
		return nil, fmt.Errorf("prometheus: read body: %w", err)
	}

	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("prometheus: server error %d: %s", resp.StatusCode, truncateStr(string(body), 200))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("prometheus: client error %d: %s", resp.StatusCode, truncateStr(string(body), 200))
	}

	var ar apiResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		// Might be a plain-text ready endpoint.
		return body, nil
	}
	if ar.Status == "error" {
		return nil, fmt.Errorf("prometheus: API error %s: %s", ar.ErrorType, ar.Error)
	}
	return ar.Data, nil
}

// ─── Parsing helpers ──────────────────────────────────────────────────────────

func (sp samplePair) parse() (time.Time, float64, error) {
	var ts float64
	if err := json.Unmarshal(sp[0], &ts); err != nil {
		return time.Time{}, 0, fmt.Errorf("parse timestamp: %w", err)
	}
	var valStr string
	if err := json.Unmarshal(sp[1], &valStr); err != nil {
		return time.Time{}, 0, fmt.Errorf("parse value: %w", err)
	}
	v, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("parse float %q: %w", valStr, err)
	}
	t := time.Unix(int64(ts), int64((ts-float64(int64(ts)))*1e9)).UTC()
	return t, v, nil
}

func matrixToPoints(entries []matrixEntry) []model.Point {
	if len(entries) == 0 {
		return nil
	}
	// Merge all series (e.g. after a sum()) into one. In practice, default
	// queries use sum() so there will be exactly one series.
	var pts []model.Point
	for _, entry := range entries {
		for _, sp := range entry.Values {
			t, v, err := sp.parse()
			if err != nil {
				continue
			}
			pts = append(pts, model.Point{T: t, V: v})
		}
	}
	return pts
}

func formatTS(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixNano())/1e9, 'f', 3, 64)
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
