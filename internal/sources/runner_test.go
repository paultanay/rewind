package sources

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/paultanay/rewind/internal/model"
)

type fakeCollector struct {
	name   string
	result CollectResult
	err    error
	called bool
}

func (f *fakeCollector) Name() string { return f.name }

func (f *fakeCollector) Collect(context.Context, model.Scope, model.TimeRange) (CollectResult, error) {
	f.called = true
	return f.result, f.err
}

func (f *fakeCollector) Check(context.Context) error { return nil }

func TestRunAllClassifiesPartialResults(t *testing.T) {
	t.Parallel()
	collectors := []Collector{
		&fakeCollector{
			name: "partial",
			result: CollectResult{
				Events:     []model.Event{{ID: "event-1"}},
				RawFixture: []byte(`{"source":"partial"}`),
			},
			err: errors.New("one endpoint failed"),
		},
		&fakeCollector{name: "failed", err: errors.New("unreachable")},
		&fakeCollector{name: "ok", result: CollectResult{Signals: []model.Signal{{ID: "signal-1"}}}},
	}

	result := RunAll(context.Background(), collectors, model.Scope{}, model.TimeRange{}, time.Second)
	if got := result.Reports[0].Status; got != model.SourceStatusPartial {
		t.Fatalf("partial source status = %q, want %q", got, model.SourceStatusPartial)
	}
	if got := result.Reports[1].Status; got != model.SourceStatusFailed {
		t.Fatalf("empty failed source status = %q, want %q", got, model.SourceStatusFailed)
	}
	if got := result.Reports[2].Status; got != model.SourceStatusOK {
		t.Fatalf("successful source status = %q, want %q", got, model.SourceStatusOK)
	}
	fixture, recognized, err := DecodeFixture(result.RawSources["partial"])
	if err != nil || !recognized || fixture.Source != "partial" || string(fixture.Raw) != `{"source":"partial"}` {
		t.Fatalf("partial raw fixture was not retained in replay envelope: recognized=%v err=%v fixture=%#v", recognized, err, fixture)
	}
}

func TestRunAllPreservesRegistrationOrder(t *testing.T) {
	t.Parallel()
	collectors := []Collector{
		&fakeCollector{name: "zeta"},
		&fakeCollector{name: "alpha"},
	}
	result := RunAll(context.Background(), collectors, model.Scope{}, model.TimeRange{}, time.Second)
	if len(result.Reports) != 2 || result.Reports[0].Name != "zeta" || result.Reports[1].Name != "alpha" {
		t.Fatalf("reports changed registration order: %#v", result.Reports)
	}
}
