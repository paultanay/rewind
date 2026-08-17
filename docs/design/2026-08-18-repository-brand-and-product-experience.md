# Rewind repository and product experience

Status: Draft for review  
Date: 2026-08-18

## Purpose

Rewind should present itself as a credible open-source incident investigation
tool: technically conservative, visually coherent, easy to evaluate, and
honest about what its evidence and rules can prove. This work covers the
repository surface as well as the investigation screen. It does not change
the incident contract or quietly expand the product into a hosted platform.

## Evidence from the current repository

- The UI is served from one embedded `internal/server/ui/dist/index.html` file.
- The page currently owns its CSS, markup, rendering, formatting, and interaction
  code in one document. This makes visual iteration and review unnecessarily
  difficult.
- The visual language is a dark dashboard with several competing accent colors,
  heavy gradients, generic cards, and text glyphs used as event icons.
- The current screen exposes useful evidence, but the operator journey is not
  explicit enough: incident state, confidence, evidence provenance, and the
  next investigation action compete for attention.
- Documentation has useful technical material, but the public entry point does
  not yet provide the product narrative, architecture visual, screenshots,
  supported deployment paths, limitations, or contributor guidance expected of
  a serious open-source project.
- `docs/superpowers/` contains internal planning artifacts. Those files are not
  product documentation and must not remain in the public repository.

## Product direction

The approved direction is a serious, data-dense operations console with the
clarity and restraint of established observability products. Rewind should be
recognisable without relying on neon decoration or generic “AI dashboard”
patterns.

The product promise is:

> Rewind turns a bounded production window into a reviewable chain of evidence.

That promise has three obligations:

1. Every conclusion must distinguish observed evidence from a ranked hypothesis.
2. Every source must show its collection status and provenance.
3. The interface must help an engineer answer “what changed, when, and what
   supports that conclusion?” without forcing them to decode the UI.

## Design system

### Brand

- Keep the name `Rewind` and create a small, reusable SVG mark based on a
  reversible timeline/loop motif.
- Use the mark consistently in the CLI documentation, web UI, favicon, and
  repository assets. Do not use arbitrary emoji or text characters as brand
  substitutes.
- Use plain, precise language. Avoid inflated claims such as “AI-powered root
  cause” or “guaranteed diagnosis”.

### Visual language

- Use a neutral canvas, a restrained blue/teal accent, and semantic red/amber
  states only where severity requires them.
- Define color, spacing, typography, border, focus, and elevation tokens in one
  place. Do not scatter literal values through components.
- Prefer a quiet surface hierarchy, crisp borders, and meaningful grouping over
  gradients and large decorative shadows.
- Use a readable sans-serif system stack with a deliberate monospace treatment
  only for IDs, timestamps, queries, and raw evidence.
- Use a consistent SVG icon set with accessible labels. Icons must reinforce
  meaning and never be the only representation of state.
- Support dark and light themes only if both can be verified. Otherwise ship one
  polished theme and document the choice rather than shipping a weak toggle.
- Meet keyboard navigation, visible focus, reduced-motion, contrast, and narrow
  viewport requirements.

## Investigation experience

The first screen should follow the incident-review workflow:

1. **Context header** — incident ID, time window, namespace/service scope,
   source coverage, and an explicit overall state.
2. **Evidence summary** — counts are secondary to a concise explanation of
   whether a high-confidence trigger exists and why.
3. **Causal ranking** — ranked hypotheses show confidence, rule IDs, trigger,
   supporting chain, and limitations. “No trigger found” is a first-class
   result, not an empty card.
4. **Replay timeline** — a readable time axis with event severity, source, and
   selected evidence. Scrubbing changes the focused window without hiding the
   full incident context.
5. **Signal and entity views** — anomalies are grouped by canonical identity;
   charts show enough scale and labels to support review rather than decoration.
6. **Evidence inspector** — selected evidence includes source, query/reference,
   timestamp, raw excerpt, and a clear indication of whether it is observed,
   derived, or synthetic.
7. **Source health** — partial and failed sources remain visible with actionable
   error text. A green summary must never imply complete coverage when a source
   failed.

Loading, empty, malformed-bundle, partial-source, and fatal-error states will
be designed explicitly and tested against fixtures. The UI will not invent
missing data to make a screen look complete.

## UI implementation approach

Use a small maintainable web source tree rather than continuing to edit a
single generated-looking HTML file:

```text
web/
  package.json
  src/
    app.ts
    styles/
      tokens.css
      base.css
      layout.css
      components.css
    components/
    icons/
    fixtures/
internal/server/ui/dist/   # generated output embedded by Go
```

The frontend build must be deterministic and produce the existing embedded
artifact. The Go server remains the owner of bundle loading and HTTP behavior;
the browser remains a presentation layer over the versioned incident response.
No UI change may weaken offline replay or introduce a network call from the
bundle viewer.

The initial implementation should keep runtime dependencies small. A frontend
framework is justified only if it materially improves maintainability and can
be built and tested reproducibly in CI. The repository must not add a large
toolchain merely to imitate another project’s stack.

## Documentation information architecture

The public documentation will be organised around the reader’s job:

- `README.md`: product statement, screenshot, supported sources, five-minute
  demo, real-stack quick start, limitations, architecture link, and contribution
  links.
- `docs/getting-started.md`: install, demo, first investigation, and offline
  replay.
- `docs/architecture.md`: component diagram, data flow, trust boundaries, and
  deterministic analysis contract.
- `docs/investigation-workflow.md`: how an engineer reads a verdict, timeline,
  source health, and evidence inspector.
- `docs/config-reference.md`: complete configuration with defaults, environment
  overrides, security notes, and examples.
- `docs/sources/`: source prerequisites, query behavior, permissions, failure
  modes, and representative output.
- `docs/bundle-spec.md`: portable bundle format and replay guarantees.
- `docs/operations.md`: CI gating, retention, sensitive data, troubleshooting,
  and upgrade notes.
- `CONTRIBUTING.md`, `SECURITY.md`, and `CHANGELOG.md`: concise, project-specific
  governance and release information.

Add a small set of repository-owned SVG diagrams and verified screenshots under
`docs/assets/`. Diagrams will be source-controlled, legible in Markdown, and
kept free of live-environment secrets. Screenshots must come from the shipped
UI and be labelled as demo data.

## Repository and licensing baseline

- Remove `docs/superpowers/` and all other internal process artifacts from the
  product branch.
- Keep commit messages short, factual, and scoped to the actual change. Avoid
  narrative claims about “production readiness” unless the evidence is in the
  repository.
- Keep PR descriptions focused on behavior, compatibility, verification, and
  known limitations.
- Add repository hygiene checks for generated binaries, local bundles, secrets,
  stale screenshots, and unsupported toolchain references.
- Apache License 2.0 is the recommended public-project license because it adds
  an explicit patent grant and is familiar to infrastructure and enterprise
  adopters. Before changing from MIT, confirm that all existing copyright
  holders have approved the change; a license change cannot be imposed on
  contributions whose rights are not controlled by the project owner. Update
  `LICENSE`, README badges/links, headers, and contributor guidance together.

## Non-goals

- No hosted control plane, account system, or telemetry service.
- No claim that deterministic correlation replaces human incident review.
- No broad rewrite of collectors or analysis rules as part of visual polish.
- No generated placeholder images, fake customer logos, or fabricated usage
  metrics.
- No removal of useful technical documentation merely to make the README shorter.

## Acceptance criteria

The work is ready for review only when:

- `docs/superpowers/` is absent from the repository and no internal process
  references are exposed.
- The UI has a coherent brand, reusable tokens, real icons, explicit states,
  responsive layout, keyboard focus, and no console/runtime errors in the demo
  and practical bundle flows.
- The same fixture renders consistently from `rewind demo --ui` and
  `rewind ui <bundle>` without network access.
- Documentation contains a working quick start, architecture diagram, product
  screenshot, source/config references, security guidance, and honest limits.
- License metadata is consistent and legally reviewed by the project owner.
- `gofmt`, `go vet ./...`, `go test -count=1 ./...`, frontend checks, build,
  lint, demo, bundle export/import, and the Docker practical scenario are
  recorded with exact commands and results.

