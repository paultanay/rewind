package loki

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/paultanay/rewind/internal/model"
)

func TestQueryRangeConvertsLokiMetricSecondsToTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1700000000,"1.5"],[1700000060,"2.5"]]}]}}`))
	}))
	defer server.Close()

	c := New(Config{URL: server.URL}, "test")
	points, err := c.queryRange(context.Background(), model.TimeRange{From: time.Unix(1700000000, 0), To: time.Unix(1700000120, 0)}, time.Minute, "sum(rate({app=\"payments\"}[1m]))")
	if err != nil {
		t.Fatalf("queryRange returned error: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("got %d points, want 2", len(points))
	}
	want := time.Unix(1700000000, 0)
	if !points[0].T.Equal(want) {
		t.Fatalf("first point timestamp = %s, want %s", points[0].T, want)
	}
}
