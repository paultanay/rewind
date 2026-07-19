// Package terminal renders an Incident as a chronological timeline on the
// terminal. This is Rewind's flagship output — the screenshot that markets
// the project. Quality bar: it must look outstanding in a 80-column terminal,
// survive both colour-capable and plain-text (NO_COLOR / pipe) environments,
// and produce exactly the same bytes given the same input (snapshot-testable).
package terminal

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fatih/color"

	"github.com/paultanay/rewind/internal/model"
)

// ─── Colour palette ───────────────────────────────────────────────────────────
// We define colours once here. Tests call color.NoColor = true; the palette
// then emits plain text automatically — no special test path needed.

var (
	colCritical = color.New(color.FgHiRed, color.Bold)
	colNotable  = color.New(color.FgHiYellow)
	colInfo     = color.New(color.FgCyan)
	colDim      = color.New(color.Faint)
	colBold     = color.New(color.Bold)
	colGreen    = color.New(color.FgGreen)
	colVerdict  = color.New(color.FgHiWhite, color.Bold)
	colHigh     = color.New(color.FgHiGreen, color.Bold)
	colMedium   = color.New(color.FgYellow, color.Bold)
	colSpec     = color.New(color.FgWhite)
)

// severity glyphs shown in the leftmost column.
const (
	glyphCritical = "●" // U+25CF BLACK CIRCLE
	glyphNotable  = "◉" // U+25C9 FISHEYE
	glyphInfo     = "○" // U+25CB WHITE CIRCLE
	glyphCP       = "▲" // U+25B2 change-point marker
)

// eventKindLabel maps EventKind to a concise terminal label.
var eventKindLabel = map[model.EventKind]string{
	model.EventKindDeploy:          "DEPLOY",
	model.EventKindConfigChange:    "CONFIG",
	model.EventKindPodKilled:       "KILLED",
	model.EventKindOOMKill:         "OOMKILL",
	model.EventKindRestart:         "RESTART",
	model.EventKindScaleChange:     "SCALE",
	model.EventKindAlertFired:      "ALERT",
	model.EventKindAlertResolved:   "RESOLVED",
	model.EventKindNodePressure:    "NODE-PRESSURE",
	model.EventKindProbeFailed:     "PROBE-FAIL",
	model.EventKindLogBurst:        "LOG-BURST",
	model.EventKindTraceErrorSpike: "TRACE-ERR",
	model.EventKindCrashLoop:       "CRASH-LOOP",
	model.EventKindUnknown:         "EVENT",
}

// Options controls optional rendering behaviour.
type Options struct {
	// Width is the terminal column width. Defaults to 120.
	Width int
	// NoColor disables ANSI colour output regardless of environment.
	NoColor bool
	// TimeFormat overrides the default HH:MM:SS timestamp format.
	TimeFormat string
	// ShowSourceRef appends the source deep-link URL inline when present.
	ShowSourceRef bool
}

func (o *Options) width() int {
	if o.Width > 0 {
		return o.Width
	}
	return 120
}

func (o *Options) timeFormat() string {
	if o.TimeFormat != "" {
		return o.TimeFormat
	}
	return "15:04:05"
}

// Render writes the full incident timeline to w.
func Render(w io.Writer, inc model.Incident, opts Options) error {
	if opts.NoColor {
		color.NoColor = true
	}

	r := &renderer{w: w, opts: opts, inc: inc}
	r.renderHeader()
	r.renderSources()
	r.renderTimeline()
	r.renderVerdict()
	r.renderFooter()
	return r.err
}

// ─── renderer internals ───────────────────────────────────────────────────────

type renderer struct {
	w    io.Writer
	opts Options
	inc  model.Incident
	err  error
}

func (r *renderer) printf(format string, a ...any) {
	if r.err != nil {
		return
	}
	_, r.err = fmt.Fprintf(r.w, format, a...)
}

func (r *renderer) line(s string) {
	r.printf("%s\n", s)
}

func (r *renderer) separator(ch string) {
	r.line(strings.Repeat(ch, r.opts.width()))
}

func (r *renderer) renderHeader() {
	r.separator("─")
	title := colBold.Sprint("  rewind  ") + "│ incident timeline"
	dur := r.inc.Window.Duration().Round(time.Minute)
	window := fmt.Sprintf("%s → %s  (%s)",
		r.inc.Window.From.Format("2006-01-02 15:04:05 MST"),
		r.inc.Window.To.Format("15:04:05"),
		dur,
	)
	r.line(colVerdict.Sprint(title))
	r.printf("  Incident  : %s\n", colBold.Sprint(r.inc.ID))
	r.printf("  Window    : %s\n", window)
	if len(r.inc.Scope.Namespaces) > 0 {
		r.printf("  Namespace : %s\n", strings.Join(r.inc.Scope.Namespaces, ", "))
	}
	r.separator("─")
	r.line("")
}

func (r *renderer) renderSources() {
	if len(r.inc.Sources) == 0 {
		return
	}
	r.line(colDim.Sprint("  Sources"))
	for _, s := range r.inc.Sources {
		var statusStr string
		switch s.Status {
		case model.SourceStatusOK:
			statusStr = colGreen.Sprint("ok      ")
		case model.SourceStatusPartial:
			statusStr = colNotable.Sprint("partial ")
		case model.SourceStatusFailed:
			statusStr = colCritical.Sprint("failed  ")
		case model.SourceStatusSkipped:
			statusStr = colDim.Sprint("skipped ")
		}
		detail := fmt.Sprintf("%devt %dsig", s.EventCount, s.SignalCount)
		if s.Error != "" {
			detail = colCritical.Sprint(s.Error)
		}
		r.printf("  %-16s %s  %-12s  %s\n",
			colDim.Sprint(s.Name), statusStr, colDim.Sprint(s.Duration), detail)
	}
	r.line("")
}

// timelineItem is a unified sortable record for the timeline.
type timelineItem struct {
	at      time.Time
	isEvent bool
	event   model.Event
	isCP    bool
	signal  model.Signal
	cp      model.ChangePoint
}

func (r *renderer) buildTimeline() []timelineItem {
	var items []timelineItem

	for _, e := range r.inc.Events {
		items = append(items, timelineItem{at: e.At, isEvent: true, event: e})
	}
	for _, s := range r.inc.Signals {
		for _, cp := range s.ChangePoints {
			items = append(items, timelineItem{
				at: cp.At, isCP: true, signal: s, cp: cp,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].at.Equal(items[j].at) {
			// Events before change-points at the same timestamp.
			if items[i].isEvent != items[j].isEvent {
				return items[i].isEvent
			}
		}
		return items[i].at.Before(items[j].at)
	})
	return items
}

func (r *renderer) renderTimeline() {
	items := r.buildTimeline()
	if len(items) == 0 {
		r.line(colDim.Sprint("  (no events or anomalies in window)"))
		r.line("")
		return
	}

	r.line(colDim.Sprint("  Timeline"))
	r.line(colDim.Sprintf("  %-8s  %-2s  %-12s  %-24s  %s",
		"TIME", "!", "TYPE", "ENTITY", "DESCRIPTION"))
	r.separator("·")

	tf := r.opts.timeFormat()
	for _, item := range items {
		ts := item.at.Format(tf)

		if item.isEvent {
			r.renderEventLine(ts, item.event)
		} else if item.isCP {
			r.renderChangepointLine(ts, item.signal, item.cp)
		}
	}
	r.line("")
}

func (r *renderer) renderEventLine(ts string, e model.Event) {
	var glyph string
	var col *color.Color
	switch e.Severity {
	case model.SeverityCritical:
		glyph = glyphCritical
		col = colCritical
	case model.SeverityNotable:
		glyph = glyphNotable
		col = colNotable
	default:
		glyph = glyphInfo
		col = colInfo
	}

	label := eventKindLabel[e.Kind]
	if label == "" {
		label = string(e.Kind)
	}

	entity := shortEntity(e.EntityID)
	title := e.Title

	// Truncate title to fit the width budget.
	maxTitle := r.opts.width() - 8 - 2 - 2 - 12 - 2 - 24 - 2 - 4
	if maxTitle < 20 {
		maxTitle = 20
	}
	title = truncate(title, maxTitle)

	line := fmt.Sprintf("  %-8s  %s  %-12s  %-24s  %s",
		colDim.Sprint(ts),
		col.Sprint(glyph),
		col.Sprint(label),
		colDim.Sprint(entity),
		col.Sprint(title),
	)
	r.line(line)

	// Inline source ref URL when requested and available.
	if r.opts.ShowSourceRef && e.SourceRef.URL != "" {
		r.printf("  %-8s  %s  %s\n", "", " ", colDim.Sprint("↳ "+e.SourceRef.URL))
	}
}

func (r *renderer) renderChangepointLine(ts string, s model.Signal, cp model.ChangePoint) {
	dirStr := "↑"
	col := colNotable
	if cp.Direction == model.DirectionDown {
		dirStr = "↓"
		col = colInfo
	}

	magnitudeStr := fmt.Sprintf("%s%.1f×", dirStr, cp.Magnitude)
	sparkline := buildSparkline(s.Points, cp.At)

	entity := shortEntity(s.EntityID)
	desc := fmt.Sprintf("%s  %s  %s  conf:%.0f%%",
		col.Sprint(magnitudeStr),
		colDim.Sprint(s.Metric),
		colDim.Sprint(sparkline),
		cp.Score*100,
	)

	line := fmt.Sprintf("  %-8s  %s  %-12s  %-24s  %s",
		colDim.Sprint(ts),
		col.Sprint(glyphCP),
		col.Sprint("ANOMALY"),
		colDim.Sprint(entity),
		desc,
	)
	r.line(line)
}

func (r *renderer) renderVerdict() {
	v := r.inc.Verdict
	if v == nil {
		return
	}

	r.separator("═")
	r.line(colVerdict.Sprint("  VERDICT"))
	r.separator("═")

	if v.NoTriggerFound || len(v.Hypotheses) == 0 {
		r.line(colDim.Sprint("  No clear trigger identified."))
		if len(v.NotableAnomalies) > 0 {
			r.line(colDim.Sprint("  Notable anomalies:"))
			for _, a := range v.NotableAnomalies {
				r.printf("    • %s\n", a)
			}
		}
		r.line("")
		return
	}

	for i, h := range v.Hypotheses {
		prefix := "  "
		if i == 0 {
			prefix = "► "
		}

		var confStr string
		var confCol *color.Color
		switch h.Confidence {
		case model.ConfidenceHigh:
			confStr = "HIGH"
			confCol = colHigh
		case model.ConfidenceMedium:
			confStr = "MEDIUM"
			confCol = colMedium
		default:
			confStr = "SPECULATIVE"
			confCol = colSpec
		}

		trigger := ""
		if e := model.EventByID(r.inc.Events, h.TriggerEventID); e != nil {
			trigger = e.Title
		}

		r.printf("%s[%d] confidence: %s  rules: %s\n",
			prefix,
			i+1,
			confCol.Sprint(confStr),
			colDim.Sprint(strings.Join(h.RuleIDs, ", ")),
		)
		if trigger != "" {
			r.printf("    trigger : %s\n", colBold.Sprint(trigger))
		}
		r.printf("    reason  : %s\n", h.Explanation)

		if len(h.Chain) > 0 {
			r.line(colDim.Sprint("    chain:"))
			for _, link := range h.Chain {
				r.printf("      → %s\n", colDim.Sprint(link.Description))
			}
		}
		r.line("")
	}
}

func (r *renderer) renderFooter() {
	r.separator("─")
	r.printf("  Generated by rewind %s at %s\n",
		r.inc.Meta.RewindVersion,
		r.inc.Meta.CreatedAt.UTC().Format(time.RFC3339),
	)
	r.separator("─")
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// shortEntity produces a display-friendly entity label from an entity ID.
// "svc/shop/checkout-78f9-abc12" → "checkout" (last path component, truncated).
func shortEntity(id string) string {
	parts := strings.Split(id, "/")
	if len(parts) == 0 {
		return id
	}
	name := parts[len(parts)-1]
	// For pods, strip the deployment hash suffix: "checkout-78f9-abc12" → "checkout-78f9"
	// (keep enough to disambiguate without line wrapping)
	return truncate(name, 22)
}

// truncate clips s to maxRunes runes, appending "…" if clipped.
func truncate(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes-1]) + "…"
}

// buildSparkline produces a 10-character sparkline of the signal values around
// the change-point. It shows the shape of the signal at a glance.
// Characters: ▁▂▃▄▅▆▇█ (8 levels, Unicode block elements).
func buildSparkline(points []model.Point, around time.Time) string {
	const bars = "▁▂▃▄▅▆▇█"
	const width = 10

	if len(points) == 0 {
		return strings.Repeat("─", width)
	}

	// Find the index closest to the change-point.
	pivot := 0
	minDiff := math.MaxFloat64
	for i, p := range points {
		d := math.Abs(float64(p.T.Sub(around)))
		if d < minDiff {
			minDiff = d
			pivot = i
		}
	}

	// Grab width points centred on the pivot.
	start := pivot - width/2
	if start < 0 {
		start = 0
	}
	end := start + width
	if end > len(points) {
		end = len(points)
		start = end - width
		if start < 0 {
			start = 0
		}
	}
	window := points[start:end]

	// Normalise to 0–7.
	min, max := window[0].V, window[0].V
	for _, p := range window {
		if p.V < min {
			min = p.V
		}
		if p.V > max {
			max = p.V
		}
	}

	barRunes := []rune(bars)
	var sb strings.Builder
	for _, p := range window {
		var idx int
		if max > min {
			idx = int((p.V - min) / (max - min) * float64(len(barRunes)-1))
		}
		if idx < 0 {
			idx = 0
		}
		if idx >= len(barRunes) {
			idx = len(barRunes) - 1
		}
		sb.WriteRune(barRunes[idx])
	}
	// Pad if we have fewer than width points.
	for sb.Len()/3 < width { // rune ≈ 3 bytes for block elements
		sb.WriteRune('─')
	}
	result := []rune(sb.String())
	if len(result) > width {
		result = result[:width]
	}
	return string(result)
}
