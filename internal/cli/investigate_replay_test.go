package cli

import (
	"testing"
	"time"

	"github.com/paultanay/rewind/internal/bundle"
	"github.com/paultanay/rewind/internal/model"
	"github.com/paultanay/rewind/internal/sources"
)

func TestReplayIncidentUsesVersionedFixtures(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 7, 9, 14, 0, 0, 0, time.UTC)
	fixtureEntity := model.Entity{
		ID:          "service/shop/checkout",
		Kind:        model.EntityKindService,
		DisplayName: "checkout",
	}
	fixtureEvent := model.Event{
		ID:       "evt-fixture",
		At:       at.Add(time.Minute),
		Kind:     model.EventKindDeploy,
		EntityID: fixtureEntity.ID,
		Title:    "fixture deployment",
	}
	fixtureSignal := model.Signal{
		ID:       "sig-fixture",
		EntityID: fixtureEntity.ID,
		Metric:   model.MetricErrorRate,
	}
	fixture, err := sources.EncodeFixture("prometheus", sources.CollectResult{
		Entities: []model.Entity{fixtureEntity},
		Events:   []model.Event{fixtureEvent},
		Signals:  []model.Signal{fixtureSignal},
	})
	if err != nil {
		t.Fatalf("EncodeFixture: %v", err)
	}

	b := &bundle.Bundle{
		Incident: model.Incident{
			ID:       "incident-replay-test",
			Window:   model.TimeRange{From: at, To: at.Add(time.Hour)},
			Entities: []model.Entity{{ID: "service/shop/stale", Kind: model.EntityKindService}},
			Verdict:  &model.Verdict{},
		},
		RawSources: map[string][]byte{"prometheus": fixture},
	}

	got, err := replayIncident(b)
	if err != nil {
		t.Fatalf("replayIncident: %v", err)
	}
	if got.Verdict != nil {
		t.Fatal("replay should clear the stored verdict before analysis")
	}
	if len(got.Entities) != 1 || got.Entities[0].ID != fixtureEntity.ID {
		t.Fatalf("replayed entities = %#v, want fixture entity", got.Entities)
	}
	if len(got.Events) != 1 || got.Events[0].ID != fixtureEvent.ID {
		t.Fatalf("replayed events = %#v, want fixture event", got.Events)
	}
	if len(got.Signals) != 1 || got.Signals[0].ID != fixtureSignal.ID {
		t.Fatalf("replayed signals = %#v, want fixture signal", got.Signals)
	}
}
