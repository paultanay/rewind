package model_test

import (
	"strings"
	"testing"
	"time"

	"github.com/paultanay/rewind/internal/model"
)

func TestNewIncidentID(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 7, 9, 14, 20, 0, 0, time.UTC)
	id := model.NewIncidentID(at)
	if !strings.HasPrefix(id, "inc-20260709-142000-") {
		t.Errorf("unexpected incident ID format: %q", id)
	}
}

func TestNewStableSignalID(t *testing.T) {
	t.Parallel()
	first := model.NewStableSignalID("prometheus", "service/shop/checkout", model.MetricLatencyP99)
	second := model.NewStableSignalID("prometheus", "service/shop/checkout", model.MetricLatencyP99)
	if first == "" || first != second {
		t.Fatalf("stable signal ID is not deterministic: %q vs %q", first, second)
	}
	if first == model.NewStableSignalID("prometheus", "service/shop/payments", model.MetricLatencyP99) {
		t.Fatal("different signal identities collided")
	}
}

func TestNewEntityID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind       model.EntityKind
		ns, name   string
		wantPrefix string
	}{
		{model.EntityKindService, "shop", "checkout", "service/"},
		{model.EntityKindDeployment, "shop", "checkout", "deployment/"},
		{model.EntityKindPod, "shop", "checkout-abc", "pod/"},
		{model.EntityKindNode, "", "node-1", "node/"},
	}
	for _, tc := range cases {
		got := model.NewEntityID(tc.kind, tc.ns, tc.name)
		if !strings.HasPrefix(got, tc.wantPrefix) {
			t.Errorf("NewEntityID(%v,%q,%q) = %q, want prefix %q",
				tc.kind, tc.ns, tc.name, got, tc.wantPrefix)
		}
	}
}

func TestCanonicalEntityID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		kind              model.EntityKind
		namespace, entity string
		want              string
	}{
		{name: "service", kind: model.EntityKindService, namespace: "shop", entity: "checkout", want: "service/shop/checkout"},
		{name: "deployment", kind: model.EntityKindDeployment, namespace: "shop", entity: "checkout", want: "deployment/shop/checkout"},
		{name: "pod", kind: model.EntityKindPod, namespace: "shop", entity: "checkout-abc", want: "pod/shop/checkout-abc"},
		{name: "node", kind: model.EntityKindNode, entity: "worker-1", want: "node/worker-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := model.CanonicalEntityID(tc.kind, tc.namespace, tc.entity)
			if err != nil {
				t.Fatalf("CanonicalEntityID returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("CanonicalEntityID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeEntityID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
		kind  model.EntityKind
	}{
		{input: "service/shop/checkout", want: "service/shop/checkout", kind: model.EntityKindService},
		{input: "svc/shop/checkout", want: "service/shop/checkout", kind: model.EntityKindService},
		{input: "deploy/shop/checkout", want: "deployment/shop/checkout", kind: model.EntityKindDeployment},
		{input: "pod/shop/checkout-abc", want: "pod/shop/checkout-abc", kind: model.EntityKindPod},
		{input: "node/worker-1", want: "node/worker-1", kind: model.EntityKindNode},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, kind, err := model.NormalizeEntityID(tc.input)
			if err != nil {
				t.Fatalf("NormalizeEntityID returned error: %v", err)
			}
			if got != tc.want || kind != tc.kind {
				t.Fatalf("NormalizeEntityID = (%q, %q), want (%q, %q)", got, kind, tc.want, tc.kind)
			}
		})
	}
}

func TestNormalizeEntityIDRejectsAmbiguousInput(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"", "service/shop", "service//checkout", "node/shop/worker", "unknown/shop/thing", "service/shop/checkout/extra"} {
		if _, _, err := model.NormalizeEntityID(input); err == nil {
			t.Errorf("NormalizeEntityID(%q) succeeded; want validation error", input)
		}
	}
}

func TestNormalizeIncidentRewritesReferences(t *testing.T) {
	t.Parallel()
	inc := model.Incident{
		Entities: []model.Entity{{ID: "svc/shop/checkout", Owner: "deploy/shop/checkout"}},
		Events:   []model.Event{{EntityID: "pod/shop/checkout-abc"}},
		Signals:  []model.Signal{{EntityID: "svc/shop/checkout"}},
	}
	got := model.NormalizeIncident(inc)
	if got.Entities[0].ID != "service/shop/checkout" || got.Entities[0].Owner != "deployment/shop/checkout" {
		t.Fatalf("entity references were not normalized: %#v", got.Entities[0])
	}
	if got.Events[0].EntityID != "pod/shop/checkout-abc" || got.Signals[0].EntityID != "service/shop/checkout" {
		t.Fatalf("event/signal references were not normalized: %#v %#v", got.Events[0], got.Signals[0])
	}
}

func TestTimeRangeContains(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 7, 9, 14, 0, 0, 0, time.UTC)
	tr := model.TimeRange{From: base, To: base.Add(45 * time.Minute)}

	if !tr.Contains(base) {
		t.Error("Contains should include From boundary")
	}
	if !tr.Contains(base.Add(45 * time.Minute)) {
		t.Error("Contains should include To boundary")
	}
	if !tr.Contains(base.Add(20 * time.Minute)) {
		t.Error("Contains should include mid-window point")
	}
	if tr.Contains(base.Add(-1 * time.Second)) {
		t.Error("Contains should exclude points before From")
	}
	if tr.Contains(base.Add(46 * time.Minute)) {
		t.Error("Contains should exclude points after To")
	}
}

func TestSeverityRank(t *testing.T) {
	t.Parallel()
	if model.SeverityRank(model.SeverityCritical) <= model.SeverityRank(model.SeverityNotable) {
		t.Error("Critical should rank higher than Notable")
	}
	if model.SeverityRank(model.SeverityNotable) <= model.SeverityRank(model.SeverityInfo) {
		t.Error("Notable should rank higher than Info")
	}
}

func TestEntityByID(t *testing.T) {
	t.Parallel()
	entities := []model.Entity{
		{ID: "svc/shop/checkout", Kind: model.EntityKindService},
		{ID: "pod/shop/checkout-abc", Kind: model.EntityKindPod},
	}
	e := model.EntityByID(entities, "svc/shop/checkout")
	if e == nil || e.Kind != model.EntityKindService {
		t.Error("EntityByID failed to find known entity")
	}
	if model.EntityByID(entities, "nonexistent") != nil {
		t.Error("EntityByID should return nil for unknown id")
	}
}

func TestSignalByMetric(t *testing.T) {
	t.Parallel()
	signals := []model.Signal{
		{ID: "sig-1", EntityID: "svc/shop/checkout", Metric: model.MetricLatencyP99},
		{ID: "sig-2", EntityID: "svc/shop/checkout", Metric: model.MetricErrorRate},
	}
	s := model.SignalByMetric(signals, "svc/shop/checkout", model.MetricLatencyP99)
	if s == nil || s.ID != "sig-1" {
		t.Error("SignalByMetric failed to find correct signal")
	}
	if model.SignalByMetric(signals, "svc/shop/checkout", model.MetricCPUUsage) != nil {
		t.Error("SignalByMetric should return nil for absent metric")
	}
}

func TestConfidenceRank(t *testing.T) {
	t.Parallel()
	if model.ConfidenceRank(model.ConfidenceHigh) <= model.ConfidenceRank(model.ConfidenceMedium) {
		t.Error("High should rank above Medium")
	}
	if model.ConfidenceRank(model.ConfidenceMedium) <= model.ConfidenceRank(model.ConfidenceSpeculative) {
		t.Error("Medium should rank above Speculative")
	}
}
