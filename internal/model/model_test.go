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

func TestNewEntityID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind       model.EntityKind
		ns, name   string
		wantPrefix string
	}{
		{model.EntityKindService, "shop", "checkout", "svc/"},
		{model.EntityKindDeployment, "shop", "checkout", "deploy/"},
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
