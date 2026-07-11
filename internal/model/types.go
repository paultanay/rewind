// Package model defines the single canonical vocabulary for the entire Rewind
// product. Every component — collectors, analysis engine, renderers, bundle
// format — speaks only in these types. Source-specific knowledge lives in the
// collectors; nothing downstream ever imports a source package.
package model

import "time"

// ─── Enumerations ────────────────────────────────────────────────────────────

// EntityKind classifies nodes in the topology graph.
type EntityKind string

const (
	EntityKindService    EntityKind = "Service"
	EntityKindDeployment EntityKind = "Deployment"
	EntityKindPod        EntityKind = "Pod"
	EntityKindNode       EntityKind = "Node"
	EntityKindQueue      EntityKind = "Queue"
	EntityKindDatabase   EntityKind = "Database"
	EntityKindUnknown    EntityKind = "Unknown"
)

// EventKind is the taxonomy of discrete point-in-time occurrences Rewind
// recognises. The analysis engine's rule IDs reference these kinds directly.
type EventKind string

const (
	EventKindDeploy          EventKind = "Deploy"
	EventKindConfigChange    EventKind = "ConfigChange"
	EventKindPodKilled       EventKind = "PodKilled"
	EventKindOOMKill         EventKind = "OOMKill"
	EventKindRestart         EventKind = "Restart"
	EventKindScaleChange     EventKind = "ScaleChange"
	EventKindAlertFired      EventKind = "AlertFired"
	EventKindAlertResolved   EventKind = "AlertResolved"
	EventKindNodePressure    EventKind = "NodePressure"
	EventKindProbeFailed     EventKind = "ProbeFailed"
	EventKindLogBurst        EventKind = "LogBurst"
	EventKindTraceErrorSpike EventKind = "TraceErrorSpike"
	EventKindCrashLoop       EventKind = "CrashLoop"  // RW009 coalesced event
	EventKindUnknown         EventKind = "Unknown"
)

// Severity indicates how operationally significant an event or verdict signal is.
type Severity string

const (
	SeverityInfo     Severity = "Info"
	SeverityNotable  Severity = "Notable"
	SeverityCritical Severity = "Critical"
)

// Confidence expresses how well-supported a causal hypothesis is.
// The verdict engine calibrates this according to §10 rules.
type Confidence string

const (
	ConfidenceHigh        Confidence = "high"
	ConfidenceMedium      Confidence = "medium"
	ConfidenceSpeculative Confidence = "speculative"
)

// Direction describes which way a change-point moved.
type Direction string

const (
	DirectionUp      Direction = "Up"
	DirectionDown    Direction = "Down"
	DirectionUnknown Direction = "Unknown"
)

// ─── Canonical metric names ───────────────────────────────────────────────────
//
// Each collector maps its source-native metric names into these constants.
// The analysis engine and renderers reference only these names — never raw
// Prometheus query strings or Loki label names. Extending the set is an
// intentional, reviewed operation.

const (
	MetricLatencyP50   = "latency.p50"
	MetricLatencyP95   = "latency.p95"
	MetricLatencyP99   = "latency.p99"
	MetricErrorRate    = "error.rate"
	MetricCPUUsage     = "cpu.usage"
	MetricCPUThrottle  = "cpu.throttle"
	MetricMemoryUsage  = "memory.usage"
	MetricRestarts     = "restarts"
	MetricQueueLag     = "queue.lag"
	MetricReplicas     = "replicas"
	MetricRequestRate  = "request.rate"
	MetricDiskIO       = "disk.io"
	MetricNetworkRecv  = "network.recv"
	MetricNetworkSend  = "network.send"
	MetricLogErrorRate = "log.error.rate"
	MetricTraceErrRate = "trace.error.rate"
)

// ─── Primary domain types ─────────────────────────────────────────────────────

// TimeRange is an inclusive window [From, To].
type TimeRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Duration returns the length of the window.
func (t TimeRange) Duration() time.Duration {
	return t.To.Sub(t.From)
}

// Contains reports whether ts falls within the window (inclusive).
func (t TimeRange) Contains(ts time.Time) bool {
	return !ts.Before(t.From) && !ts.After(t.To)
}

// Scope describes the investigative perimeter the user requested.
type Scope struct {
	Namespaces []string          `json:"namespaces,omitempty"`
	Services   []string          `json:"services,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

// Entity is a node in the topology graph that Rewind constructs from
// Kubernetes ownership chains and service-level identifiers.
type Entity struct {
	ID     string            `json:"id"`
	Kind   EntityKind        `json:"kind"`
	Owner  string            `json:"owner,omitempty"` // parent entity ID
	Labels map[string]string `json:"labels,omitempty"`
	// DisplayName is a short human-friendly label for renderers.
	DisplayName string `json:"displayName,omitempty"`
}

// Point is a single sample in a time series.
type Point struct {
	T time.Time `json:"t"`
	V float64   `json:"v"`
}

// ChangePoint is a statistically detected inflection in a Signal.
// Produced by analyze/changepoint; consumed by the correlation engine.
type ChangePoint struct {
	At        time.Time `json:"at"`
	Direction Direction `json:"direction"`
	// Magnitude is the ratio of post-change mean to pre-change mean (e.g.
	// 3.4 means the signal tripled). Use absolute value; Direction carries sign.
	Magnitude float64 `json:"magnitude"`
	// Score is detector confidence in [0,1]. Higher = stronger evidence.
	Score float64 `json:"score"`
	// DetectorID identifies which algorithm produced this point, for
	// auditability (e.g. "baseline-deviation", "pelt").
	DetectorID string `json:"detectorId"`
}

// Signal is a named, entity-scoped time series plus its detected change-points.
// Points are downsampled to ≤ 500 per signal to keep bundles portable.
type Signal struct {
	ID           string        `json:"id"`
	EntityID     string        `json:"entityId"`
	Metric       string        `json:"metric"` // one of the Metric* constants
	Unit         string        `json:"unit"`
	Points       []Point       `json:"points"`
	ChangePoints []ChangePoint `json:"changePoints,omitempty"`
	// Baseline holds the pre-incident reference window points used by
	// change-point detectors. Not rendered; included in bundles for replay.
	Baseline []Point `json:"baseline,omitempty"`
}

// SourceRef provides a deep-link back to the originating system so engineers
// can navigate from a Rewind event to the raw data without leaving their tools.
type SourceRef struct {
	// SourceName matches a key in the Incident.Sources slice.
	SourceName string `json:"sourceName"`
	// NativeID is the source-system's own identifier (e.g. trace ID, alert
	// fingerprint, K8s UID).
	NativeID string `json:"nativeId,omitempty"`
	// URL is a direct browser link when the source exposes one.
	URL string `json:"url,omitempty"`
}

// Event is a discrete, point-in-time occurrence on a specific entity.
// All six source collectors produce Events; the analysis engine may synthesise
// additional ones (e.g. CrashLoop via RW009 coalescing).
type Event struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Kind     EventKind `json:"kind"`
	EntityID string    `json:"entityId"`
	Severity Severity  `json:"severity"`
	// Title is a single human-readable line, safe for terminal display.
	Title string `json:"title"`
	// Detail carries structured supplementary information (e.g. image tag,
	// diff URL, sample log lines). May be multi-line.
	Detail    string    `json:"detail,omitempty"`
	SourceRef SourceRef `json:"sourceRef,omitempty"`
}

// ─── Analysis outputs ─────────────────────────────────────────────────────────

// ChainLink is one step in a causal narrative.
type ChainLink struct {
	// Exactly one of EventID or SignalID+ChangePointIndex will be set.
	EventID          string `json:"eventId,omitempty"`
	SignalID          string `json:"signalId,omitempty"`
	ChangePointIndex int    `json:"changePointIndex,omitempty"`
	// Description is a one-line human explanation of this link's role.
	Description string `json:"description"`
	// RuleID is the rule that created this link.
	RuleID string `json:"ruleId,omitempty"`
}

// Hypothesis is one candidate causal explanation for the incident, assembled
// by the correlation engine. Multiple hypotheses are emitted in rank order.
type Hypothesis struct {
	// TriggerEventID points to the Event the engine believes started the chain.
	TriggerEventID string `json:"triggerEventId"`
	Confidence     Confidence `json:"confidence"`
	// Score is the internal numerical ranking (higher = more likely).
	// Exposed in bundles/JSON for transparency; not shown in terminal UI.
	Score       float64     `json:"score"`
	Chain       []ChainLink `json:"chain"`
	Explanation string      `json:"explanation"`
	// RuleIDs lists every rule that contributed to this hypothesis.
	RuleIDs []string `json:"ruleIds"`
}

// Verdict is the top-level output of the analysis engine.
// Hypotheses are ordered best-first. If the engine finds no chain above the
// minimum score floor, Hypotheses may be empty and NoTriggerFound is set.
type Verdict struct {
	Hypotheses     []Hypothesis `json:"hypotheses"`
	NoTriggerFound bool         `json:"noTriggerFound,omitempty"`
	// NotableAnomalies lists the most significant ChangePoints when no causal
	// chain is found, giving the engineer somewhere to start.
	NotableAnomalies []string `json:"notableAnomalies,omitempty"`
}

// ─── Incident and meta ────────────────────────────────────────────────────────

// SourceStatus records the outcome of a single collector run.
type SourceStatus string

const (
	SourceStatusOK      SourceStatus = "ok"
	SourceStatusPartial SourceStatus = "partial"
	SourceStatusFailed  SourceStatus = "failed"
	SourceStatusSkipped SourceStatus = "skipped"
)

// SourceReport summarises what a collector found (or why it didn't).
type SourceReport struct {
	Name     string       `json:"name"`
	Status   SourceStatus `json:"status"`
	Duration string       `json:"duration"` // human-readable, e.g. "1.2s"
	// EventCount and SignalCount are informational for renderers.
	EventCount  int    `json:"eventCount"`
	SignalCount int    `json:"signalCount"`
	Error       string `json:"error,omitempty"`
	// Endpoint is the URL queried, redacted of credentials.
	Endpoint string `json:"endpoint,omitempty"`
}

// Meta carries tool-level provenance so a bundle is always self-describing.
type Meta struct {
	// RewindVersion is the semver string of the tool that produced this bundle.
	RewindVersion string `json:"rewindVersion"`
	// SchemaVersion is the bundle schema version; used for forward-compatible
	// reading. Current: 1.
	SchemaVersion int `json:"schemaVersion"`
	// CreatedAt is when the investigation ran (UTC).
	CreatedAt time.Time `json:"createdAt"`
	// GeneratedBy is the hostname, for postmortem attribution.
	GeneratedBy string `json:"generatedBy,omitempty"`
}

// Incident is the root type. Every renderer, exporter, and the analysis engine
// receive one Incident and produce their output from it alone.
// The bundle format is: gzipped tar containing incident.json (this struct
// JSON-serialised) plus sources/*.json raw fixtures for replay.
type Incident struct {
	ID      string   `json:"id"`
	Window  TimeRange `json:"window"`
	Scope   Scope    `json:"scope"`
	Entities []Entity `json:"entities"`
	Events  []Event  `json:"events"`
	Signals []Signal `json:"signals"`
	Verdict *Verdict `json:"verdict,omitempty"`
	Sources []SourceReport `json:"sources"`
	Meta    Meta     `json:"meta"`
}
