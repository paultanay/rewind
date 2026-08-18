export type Severity = "Critical" | "Notable" | "Info" | string;
export type Confidence = "HIGH" | "MEDIUM" | "SPECULATIVE" | string;

export interface ChangePoint {
  at?: string;
  direction?: string;
  magnitude?: number;
  score?: number;
  detectorId?: string;
}

export interface SignalPoint {
  t?: string;
  v?: number;
}

export interface Signal {
  id?: string;
  metric?: string;
  entityId?: string;
  unit?: string;
  points?: SignalPoint[];
  changePoints?: ChangePoint[];
}

export interface Event {
  id?: string;
  at?: string;
  kind?: string;
  entityId?: string;
  severity?: Severity;
  title?: string;
  detail?: string;
  sourceRef?: { sourceName?: string; reference?: string };
}

export interface Source {
  name?: string;
  status?: "ok" | "partial" | "failed" | "skipped" | string;
  error?: string;
  eventCount?: number;
  signalCount?: number;
}

export interface Hypothesis {
  triggerEventId?: string;
  ruleIds?: string[];
  score?: number;
  confidence?: Confidence;
  explanation?: string;
  chain?: { description?: string }[];
}

export interface Incident {
  id?: string;
  window?: { from?: string; to?: string };
  scope?: { namespaces?: string[]; services?: string[] };
  entities?: { id?: string; kind?: string }[];
  events?: Event[];
  signals?: Signal[];
  sources?: Source[];
  verdict?: { hypotheses?: Hypothesis[]; noTriggerFound?: boolean };
}

export type EvidenceItem =
  | { type: "event"; at?: string; event: Event }
  | { type: "anomaly"; at?: string; signal: Signal; change: ChangePoint };
