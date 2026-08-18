import "./styles/base.css";
import "./styles/layout.css";
import "./styles/components.css";
import { icon } from "./icons";
import type { ChangePoint, Confidence, Event, EvidenceItem, Incident, Signal, Source } from "./types";

const app = document.querySelector<HTMLDivElement>("#app");
if (!app) throw new Error("Rewind mount point is missing");

app.innerHTML = `
  <div class="app-shell">
    <header class="topbar">
      <div class="topbar-left">
        <a class="brand-link" href="/" aria-label="Rewind home">
          <svg class="brand-mark" viewBox="0 0 40 40" fill="none" aria-hidden="true"><path d="M11 11h14a7 7 0 1 1 0 14H17m0 0 4-4m-4 4 4 4M29 29H15a7 7 0 1 1 0-14h8m0 0-4-4m4 4-4 4" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"/></svg>
          <span><span class="brand-wordmark">REWIND</span><span class="brand-caption">incident evidence workspace</span></span>
        </a>
        <span class="topbar-context">Review</span>
      </div>
      <div class="topbar-actions">
        <div class="topbar-status"><span id="collection-status"><span class="status-dot ok"></span> offline analysis</span><code id="top-incident-id">loading</code></div>
        <button id="theme-toggle" class="theme-toggle" type="button" aria-label="Switch theme" aria-pressed="false"><span id="theme-icon"></span><span id="theme-label">Light</span></button>
      </div>
    </header>
    <div id="loading" class="loading-state">Loading incident evidence…</div>
    <section id="fatal-error" class="fatal-state error-card" hidden aria-live="assertive"></section>
    <div id="workspace" class="workspace" hidden>
      <aside class="context-rail" aria-label="Incident context">
        <div class="rail-block">
          <div class="rail-heading"><h2>Incident</h2><span id="source-summary">—</span></div>
          <code id="incident-id" class="incident-id"></code>
        </div>
        <div class="rail-block">
          <div class="rail-heading"><h2>Assessment</h2><span id="hypothesis-summary">—</span></div>
          <div id="verdicts"></div>
        </div>
        <div class="rail-block">
          <div class="rail-heading"><h2>Source health</h2><span id="health-summary">—</span></div>
          <div id="sources"></div>
        </div>
        <p class="rail-note">Rules rank evidence; they do not replace the engineer’s review. Alerts corroborate impact and never create a trigger.</p>
      </aside>
      <main class="workspace-main">
        <section class="hero" aria-labelledby="incident-title">
          <div class="hero-copy">
            <div class="eyebrow">Investigation / replay</div>
            <h1 id="incident-title">Incident</h1>
            <p class="hero-description">Review what changed, when it changed, and which collected evidence supports the current causal ranking.</p>
            <div id="incident-window" class="meta-line"></div>
          </div>
          <div id="overall-status" class="status-pill hero-status"><span class="status-dot"></span><span>Evidence loaded</span></div>
        </section>
        <section id="summary" class="summary-grid" data-testid="incident-summary" aria-label="Incident summary"></section>
        <section class="content-section card replay-card" aria-label="Replay position">
          <div class="replay-head"><div><h2>Replay position</h2><p>Move through the incident window to focus the timeline.</p></div><span id="cursor-label" class="replay-time"></span></div>
          <input id="cursor" type="range" min="0" max="1000" value="1000" aria-label="Incident replay position" />
          <div class="scale"><span id="from-label"></span><span id="to-label"></span></div>
        </section>
        <section class="content-section" aria-labelledby="anomaly-heading">
          <div class="section-heading"><div><h2 id="anomaly-heading">Signal anomalies</h2><p>Change-points detected in the investigation window.</p></div><small id="anomaly-count"></small></div>
          <div id="anomalies" class="anomaly-grid"></div>
        </section>
        <section class="content-section" aria-labelledby="timeline-heading">
          <div class="section-heading"><div><h2 id="timeline-heading">Evidence timeline</h2><p>Select an item to inspect its provenance.</p></div></div>
          <div class="detail-grid"><div id="timeline" class="timeline" data-testid="evidence-timeline"></div><article id="evidence" class="card evidence-card evidence-panel" aria-live="polite"></article></div>
        </section>
        <section class="content-section" aria-labelledby="entity-heading">
          <div class="section-heading"><div><h2 id="entity-heading">Entity lanes</h2><p>Signals grouped by canonical identity.</p></div></div>
          <div id="lanes" class="lane-list"></div>
        </section>
      </main>
    </div>
  </div>`;

type ElementId =
  | "loading" | "fatal-error" | "workspace" | "top-incident-id" | "collection-status" | "theme-toggle" | "theme-icon" | "theme-label"
  | "source-summary" | "incident-id" | "hypothesis-summary" | "verdicts" | "health-summary"
  | "sources" | "incident-title" | "overall-status" | "incident-window" | "summary" | "cursor"
  | "cursor-label" | "from-label" | "to-label" | "anomaly-count" | "anomalies" | "timeline" | "evidence" | "lanes";

const $ = <T extends HTMLElement = HTMLElement>(id: ElementId): T => {
  const node = document.getElementById(id);
  if (!node) throw new Error(`Missing UI element: ${id}`);
  return node as T;
};

type Theme = "light" | "dark";
const themeStorageKey = "rewind.theme";

function applyTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
  const nextTheme: Theme = theme === "dark" ? "light" : "dark";
  const toggle = $("theme-toggle");
  toggle.setAttribute("aria-label", `Switch to ${nextTheme} theme`);
  toggle.setAttribute("aria-pressed", String(theme === "dark"));
  text($("theme-label"), nextTheme === "dark" ? "Dark" : "Light");
  $("theme-icon").innerHTML = icon(nextTheme === "dark" ? "moon" : "sun");
}

function initialiseTheme(): void {
  let saved: string | null = null;
  try { saved = window.localStorage.getItem(themeStorageKey); } catch { /* storage can be unavailable in locked-down browsers */ }
  const theme: Theme = saved === "dark" || saved === "light"
    ? saved
    : window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  applyTheme(theme);
  $("theme-toggle").addEventListener("click", () => {
    const next: Theme = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
    applyTheme(next);
    try { window.localStorage.setItem(themeStorageKey, next); } catch { /* preference remains active for this page */ }
  });
}

const list = <T>(value: T[] | undefined): T[] => Array.isArray(value) ? value : [];
const text = (node: Node, value: unknown): void => { node.textContent = value == null ? "" : String(value); };

function add<K extends keyof HTMLElementTagNameMap>(parent: Element, tag: K, className?: string, value?: string): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (value !== undefined) node.textContent = value;
  parent.append(node);
  return node;
}

function safeDate(value: unknown): Date | null {
  if (typeof value === "number") return new Date(value);
  if (typeof value !== "string" || !value) return null;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

function formatTime(value: unknown, withZone = true): string {
  const date = safeDate(value);
  if (!date) return "Unknown time";
  return new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false, timeZoneName: withZone ? "short" : undefined }).format(date);
}

function formatShort(value: unknown): string { return formatTime(value, false); }
function confidenceClass(value: Confidence | undefined): string { return String(value ?? "").toLowerCase(); }
function statusClass(value: Source["status"]): string { return ["ok", "partial", "failed"].includes(String(value)) ? String(value) : "skipped"; }
function severityClass(value: string | undefined): string { return String(value ?? "").toLowerCase() === "critical" ? "critical" : String(value ?? "").toLowerCase() === "notable" ? "notable" : ""; }
function eventIconName(kind: string | undefined): string { return ({ Deploy: "layers", ConfigChange: "activity", OOMKill: "alert", Restart: "server", PodKilled: "alert", NodePressure: "activity", AlertFired: "alert", AlertResolved: "activity", LogBurst: "search", TraceErrorSpike: "activity", CrashLoop: "alert" } as Record<string, string>)[kind ?? ""] ?? "spark"; }
function sourceStatusLabel(value: Source["status"]): string { return value === "ok" ? "available" : value === "partial" ? "partial data" : value === "failed" ? "unavailable" : "not queried"; }
function eventIndex(incident: Incident): Record<string, Event> { return Object.fromEntries(list(incident.events).filter(event => event.id).map(event => [event.id, event])); }
function entityIndex(incident: Incident): Record<string, { id?: string; kind?: string }> { return Object.fromEntries(list(incident.entities).filter(entity => entity.id).map(entity => [entity.id, entity])); }

function renderSources(incident: Incident): void {
  const sources = list(incident.sources); const target = $("sources"); target.replaceChildren();
  let healthy = 0;
  for (const source of sources) {
    if (source.status === "ok") healthy++;
    const row = add(target, "div", `source-row ${statusClass(source.status)}`);
    add(row, "span", `status-dot ${statusClass(source.status)}`);
    const copy = add(row, "div");
    add(copy, "strong", "source-name", source.name || "Unnamed source");
    add(copy, "span", "source-detail", `${sourceStatusLabel(source.status)}${source.error ? ` · ${source.error}` : ""}`);
    add(row, "span", "source-count", `${source.eventCount ?? 0} events · ${source.signalCount ?? 0} signals`);
  }
  if (!sources.length) add(target, "div", "empty-state", "No configured sources contributed data.");
  text($("health-summary"), sources.length ? `${healthy}/${sources.length} ready` : "none");
  text($("source-summary"), `${sources.length} source${sources.length === 1 ? "" : "s"}`);
}

function renderVerdict(incident: Incident): void {
  const target = $("verdicts"); target.replaceChildren();
  const hypotheses = list(incident.verdict?.hypotheses); text($("hypothesis-summary"), `${hypotheses.length} ranked`);
  if (!hypotheses.length) {
    const empty = add(target, "div", "empty-state card-flat");
    add(empty, "strong", undefined, incident.verdict?.noTriggerFound ? "No trigger identified" : "No hypotheses available");
    add(empty, "span", undefined, incident.verdict?.noTriggerFound ? "Start with the notable anomalies in the timeline." : "The collected window did not produce a ranked hypothesis.");
    return;
  }
  const events = eventIndex(incident);
  hypotheses.slice(0, 3).forEach((hypothesis, index) => {
    const card = add(target, "article", "verdict-card card");
    const top = add(card, "div", "verdict-top");
    const confidence = add(top, "span", `confidence-pill ${confidenceClass(hypothesis.confidence)}`);
    add(confidence, "span", "status-dot ok"); add(confidence, "span", undefined, hypothesis.confidence || "Unrated");
    add(top, "span", "rank-label", `Rank ${index + 1}`);
    const trigger = hypothesis.triggerEventId ? events[hypothesis.triggerEventId] : undefined;
    add(card, "h3", "verdict-trigger", trigger?.title || hypothesis.triggerEventId || "Unknown trigger");
    if (hypothesis.explanation) add(card, "p", "verdict-explanation", hypothesis.explanation);
    const rules = add(card, "div", "rule-list"); list(hypothesis.ruleIds).forEach(rule => add(rules, "span", "rule-tag", rule));
    const chain = list(hypothesis.chain); if (chain.length) { const chainList = add(card, "ul", "chain-list"); chain.forEach(link => add(chainList, "li", undefined, link.description || "Supporting evidence")); }
  });
}

function anomalyItems(incident: Incident): EvidenceItem[] {
  const items: EvidenceItem[] = [];
  for (const signal of list(incident.signals)) for (const change of list(signal.changePoints)) if ((change.score ?? 0) >= .3) items.push({ type: "anomaly", at: change.at, signal, change });
  return items.sort((a, b) => new Date(a.at ?? 0).getTime() - new Date(b.at ?? 0).getTime());
}

function renderSummary(incident: Incident): void {
  const target = $("summary"); target.replaceChildren();
  const critical = list(incident.events).filter(event => event.severity === "Critical").length;
  const anomalies = anomalyItems(incident).length;
  const metrics: [string, number, string][] = [["Entities", list(incident.entities).length, "blue"], ["Events", list(incident.events).length, "brand"], ["Anomalies", anomalies, "warning"], ["Sources", list(incident.sources).length, "blue"]];
  for (const [label, value, color] of metrics) { const card = add(target, "div", "card metric-card"); add(card, "span", "metric-label", label); add(card, "strong", color, String(value)); }
  const status = $("overall-status"); status.className = `status-pill ${critical ? "critical" : "high"}`; status.replaceChildren(); add(status, "span", `status-dot ${critical ? "failed" : "ok"}`); add(status, "span", undefined, critical ? `${critical} critical event${critical === 1 ? "" : "s"}` : "Evidence loaded");
}

function renderAnomalies(incident: Incident): void {
  const target = $("anomalies"); target.replaceChildren(); const anomalies = anomalyItems(incident).sort((a, b) => (b.type === "anomaly" ? b.change.score ?? 0 : 0) - (a.type === "anomaly" ? a.change.score ?? 0 : 0));
  text($("anomaly-count"), `${anomalies.length} detected`);
  if (!anomalies.length) { const empty = add(target, "div", "card empty-state"); add(empty, "strong", undefined, "No notable change-points"); add(empty, "span", undefined, "No signal crossed the configured detector threshold in this window."); return; }
  anomalies.slice(0, 12).forEach(item => { if (item.type !== "anomaly") return; const card = add(target, "article", "card anomaly-card"); add(card, "div", "anomaly-title", item.signal.metric || "unnamed signal"); add(card, "div", "anomaly-entity", item.signal.entityId || "unknown entity"); const values = add(card, "div", "anomaly-values"); const direction = item.change.direction === "Up" ? "↑" : item.change.direction === "Down" ? "↓" : "→"; add(values, "span", undefined, `${direction} ${(item.change.magnitude ?? 0).toFixed(1)}×`); add(values, "span", undefined, `${Math.round((item.change.score ?? 0) * 100)}% confidence`); });
}

function itemKey(item: EvidenceItem): string { return item.type === "event" ? `event:${item.event.id}` : `anomaly:${item.signal.id}:${item.change.at}`; }
function buildItems(incident: Incident): EvidenceItem[] { return [...list(incident.events).map(event => ({ type: "event" as const, at: event.at, event })), ...anomalyItems(incident)].sort((a, b) => new Date(a.at ?? 0).getTime() - new Date(b.at ?? 0).getTime()); }

function renderEvidence(item: EvidenceItem | undefined): void {
  const target = $("evidence"); target.replaceChildren();
  if (!item) { add(target, "div", "empty-state", "Select an evidence item to inspect it."); return; }
  add(target, "div", "evidence-label", item.type === "event" ? "Observed event" : "Derived signal evidence");
  if (item.type === "event") {
    add(target, "h3", undefined, item.event.title || item.event.kind || "Event");
    const data = add(target, "dl", "evidence-data");
    [["time", formatTime(item.event.at)], ["kind", item.event.kind], ["severity", item.event.severity], ["entity", item.event.entityId], ["source", item.event.sourceRef?.sourceName]].forEach(([key, value]) => { add(data, "dt", undefined, key); add(data, "dd", undefined, value || "Not recorded"); });
    if (item.event.detail) add(target, "pre", "raw-evidence", item.event.detail);
  } else {
    add(target, "h3", undefined, item.signal.metric || "Signal change-point");
    const data = add(target, "dl", "evidence-data");
    [["time", formatTime(item.change.at)], ["entity", item.signal.entityId], ["direction", item.change.direction], ["magnitude", `${(item.change.magnitude ?? 0).toFixed(2)}×`], ["confidence", `${Math.round((item.change.score ?? 0) * 100)}%`], ["detector", item.change.detectorId]].forEach(([key, value]) => { add(data, "dt", undefined, key); add(data, "dd", undefined, value || "Not recorded"); });
  }
}

function renderTimeline(incident: Incident): void {
  const target = $("timeline"); target.replaceChildren(); const items = buildItems(incident); const selected = { value: items[0] as EvidenceItem | undefined };
  if (!items.length) { add(target, "div", "card empty-state", "No events or signal anomalies exist in this window."); renderEvidence(undefined); return; }
  const updateSelection = (item: EvidenceItem): void => { selected.value = item; renderEvidence(item); target.querySelectorAll<HTMLButtonElement>(".timeline-item").forEach(node => node.classList.toggle("selected", node.dataset.key === itemKey(item))); };
  items.forEach(item => { const event = item.type === "event" ? item.event : undefined; const button = add(target, "button", "timeline-item") as HTMLButtonElement; button.type = "button"; button.dataset.key = itemKey(item); button.dataset.at = String(new Date(item.at ?? 0).getTime()); button.addEventListener("click", () => updateSelection(item)); add(button, "time", "timeline-time", formatShort(item.at)); const marker = add(button, "span", `event-marker ${event ? severityClass(event.severity) : "notable"}`); marker.innerHTML = icon(event ? eventIconName(event.kind) : "activity"); const body = add(button, "span"); add(body, "strong", "timeline-title", event?.title || `${item.type === "anomaly" ? item.change.direction || "Change" : "Evidence"} ${item.type === "anomaly" ? item.signal.metric || "signal" : ""}`); const meta = add(body, "span", "timeline-meta"); add(meta, "code", undefined, event?.entityId || (item.type === "anomaly" ? item.signal.entityId : "unknown entity") || "unknown entity"); add(meta, "span", undefined, event?.kind || (item.type === "anomaly" ? `${Math.round((item.change.score ?? 0) * 100)}% confidence` : "evidence")); });
  renderEvidence(selected.value); const first = target.querySelector<HTMLButtonElement>(".timeline-item"); first?.classList.add("selected");
}

function sparkline(signal: Signal): SVGSVGElement {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg"); svg.setAttribute("class", "sparkline"); svg.setAttribute("viewBox", "0 0 240 44"); svg.setAttribute("role", "img"); svg.setAttribute("aria-label", `${signal.metric || "Signal"} trend`);
  const values = list(signal.points).map(point => Number(point.v)).filter(Number.isFinite); if (!values.length) return svg;
  const min = Math.min(...values); const span = Math.max(Math.max(...values) - min, 1); const line = document.createElementNS("http://www.w3.org/2000/svg", "polyline"); line.setAttribute("fill", "none"); line.setAttribute("stroke", "currentColor"); line.setAttribute("stroke-width", "2"); line.setAttribute("stroke-linecap", "round"); line.setAttribute("stroke-linejoin", "round"); line.setAttribute("points", values.map((value, index) => `${(index / Math.max(values.length - 1, 1)) * 238 + 1},${42 - ((value - min) / span) * 38}`).join(" ")); svg.append(line); return svg;
}

function renderLanes(incident: Incident): void {
  const target = $("lanes"); target.replaceChildren(); const entities = entityIndex(incident); const grouped = new Map<string, Signal[]>();
  list(incident.signals).forEach(signal => { const id = signal.entityId || "unknown entity"; const group = grouped.get(id) ?? []; group.push(signal); grouped.set(id, group); });
  if (!grouped.size) { add(target, "div", "card empty-state", "No signal series were returned by the configured sources."); return; }
  [...grouped.keys()].sort().forEach(id => { const lane = add(target, "div", "card lane-card"); const name = add(lane, "div", "lane-name", id); add(name, "span", "lane-kind", entities[id]?.kind || "entity"); const chart = add(lane, "div"); grouped.get(id)?.slice(0, 3).forEach((signal, index) => { const row = add(chart, "div"); const label = add(row, "div", "timeline-meta", `${signal.metric || "signal"}${signal.unit ? ` · ${signal.unit}` : ""}`); if (index) label.style.marginTop = "6px"; row.append(sparkline(signal)); }); });
}

function render(incident: Incident): void {
  const from = safeDate(incident.window?.from); const to = safeDate(incident.window?.to); const fromMs = from?.getTime() ?? Date.now(); const toMs = to?.getTime() ?? fromMs + 1;
  text($("top-incident-id"), incident.id || "unknown incident"); text($("incident-id"), incident.id || "unknown incident"); text($("incident-title"), incident.id || "Incident");
  const meta = $("incident-window"); meta.replaceChildren(); add(meta, "span", undefined, `${formatTime(incident.window?.from)} → ${formatTime(incident.window?.to)}`); const namespaces = list(incident.scope?.namespaces); if (namespaces.length) add(meta, "span", undefined, `namespace: ${namespaces.join(", ")}`); const services = list(incident.scope?.services); if (services.length) add(meta, "span", undefined, `services: ${services.join(", ")}`);
  text($("from-label"), formatShort(fromMs)); text($("to-label"), formatShort(toMs)); text($("cursor-label"), formatTime(toMs));
  const cursor = $("cursor") as HTMLInputElement; const updateCursor = (): void => { const point = fromMs + (toMs - fromMs) * (Number(cursor.value) / 1000); text($("cursor-label"), formatTime(point)); $("timeline").querySelectorAll<HTMLElement>(".timeline-item").forEach(item => item.classList.toggle("past", Number(item.dataset.at) <= point)); }; cursor.addEventListener("input", updateCursor);
  renderSources(incident); renderVerdict(incident); renderSummary(incident); renderAnomalies(incident); renderTimeline(incident); renderLanes(incident); updateCursor();
}

async function load(): Promise<void> {
  try {
    const response = await fetch("/api/incident", { headers: { Accept: "application/json" } });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const incident = await response.json() as Incident;
    $("loading").hidden = true; $("workspace").hidden = false; render(incident);
  } catch (error) {
    $("loading").hidden = true; const target = $("fatal-error"); target.hidden = false; target.textContent = `Unable to load incident evidence: ${error instanceof Error ? error.message : "unknown error"}`;
  }
}

initialiseTheme();
void load();
