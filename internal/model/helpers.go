package model

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// NewIncidentID generates a short, time-prefixed ID suitable for use as a
// filename component and as a human-readable reference.
// Format: inc-20060102-150405-<short-uuid>
func NewIncidentID(at time.Time) string {
	short := uuid.New().String()[:8]
	return fmt.Sprintf("inc-%s-%s", at.UTC().Format("20060102-150405"), short)
}

// NewEventID returns a stable unique ID for an Event.
func NewEventID() string {
	return "evt-" + uuid.New().String()[:12]
}

// NewSignalID returns a stable unique ID for a Signal.
func NewSignalID() string {
	return "sig-" + uuid.New().String()[:12]
}

// NewEntityID constructs a deterministic, human-readable entity ID from its
// kind and namespace/name. Using a deterministic scheme means the topology
// graph edges survive bundle round-trips without fixup.
// Example: "svc/shop/checkout", "pod/shop/checkout-7d9f-abc12"
func NewEntityID(kind EntityKind, namespace, name string) string {
	switch kind {
	case EntityKindService:
		return fmt.Sprintf("svc/%s/%s", namespace, name)
	case EntityKindDeployment:
		return fmt.Sprintf("deploy/%s/%s", namespace, name)
	case EntityKindPod:
		return fmt.Sprintf("pod/%s/%s", namespace, name)
	case EntityKindNode:
		return fmt.Sprintf("node/%s", name)
	case EntityKindQueue:
		return fmt.Sprintf("queue/%s/%s", namespace, name)
	case EntityKindDatabase:
		return fmt.Sprintf("db/%s/%s", namespace, name)
	default:
		return fmt.Sprintf("entity/%s/%s", namespace, name)
	}
}

// SeverityRank returns a numeric rank so severities can be compared / sorted.
// Higher = more severe.
func SeverityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityNotable:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

// ConfidenceRank returns a numeric rank for confidence levels.
func ConfidenceRank(c Confidence) int {
	switch c {
	case ConfidenceHigh:
		return 3
	case ConfidenceMedium:
		return 2
	case ConfidenceSpeculative:
		return 1
	default:
		return 0
	}
}

// EntityByID finds an entity by ID in the slice, returning nil if absent.
// O(n) — slices are small enough that a map is unnecessary overhead.
func EntityByID(entities []Entity, id string) *Entity {
	for i := range entities {
		if entities[i].ID == id {
			return &entities[i]
		}
	}
	return nil
}

// EventByID finds an event by ID in the slice.
func EventByID(events []Event, id string) *Event {
	for i := range events {
		if events[i].ID == id {
			return &events[i]
		}
	}
	return nil
}

// SignalByID finds a signal by ID.
func SignalByID(signals []Signal, id string) *Signal {
	for i := range signals {
		if signals[i].ID == id {
			return &signals[i]
		}
	}
	return nil
}

// EventsForEntity returns all events whose EntityID matches id.
func EventsForEntity(events []Event, id string) []Event {
	var out []Event
	for _, e := range events {
		if e.EntityID == id {
			out = append(out, e)
		}
	}
	return out
}

// SignalsForEntity returns all signals whose EntityID matches id.
func SignalsForEntity(signals []Signal, id string) []Signal {
	var out []Signal
	for _, s := range signals {
		if s.EntityID == id {
			out = append(out, s)
		}
	}
	return out
}

// SignalByMetric returns the first signal for a given entity and metric name,
// or nil if none exists. Useful in correlation rules.
func SignalByMetric(signals []Signal, entityID, metric string) *Signal {
	for i := range signals {
		if signals[i].EntityID == entityID && signals[i].Metric == metric {
			return &signals[i]
		}
	}
	return nil
}
