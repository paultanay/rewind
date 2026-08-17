# Rewind repository and product experience implementation plan

**Goal:** Ship a branded, maintainable, verifiable Rewind repository with a professional incident-review UI, credible documentation, consistent licensing, and no internal workflow artifacts.

**Architecture:** Keep the Go server, incident model, collectors, analysis rules, bundle format, and offline guarantees stable. Move the presentation layer into a small TypeScript/CSS frontend built to the embedded `internal/server/ui/dist` directory; keep the browser as a read-only renderer of `/api/incident`. Organise public documentation around installation, investigation, architecture, sources, operations, and contribution.

**Tech Stack:** Go 1.25, existing Go tests and embedded `embed.FS`, TypeScript, Vite, CSS custom properties, inline SVG icons/diagrams, npm lockfile, GitHub Actions.

---

### Task 1: Repository hygiene and license baseline

**Files:**

- Modify: `LICENSE`
- Modify: `README.md`
- Modify: `CONTRIBUTING.md`
- Modify: `SECURITY.md`
- Modify: `.gitignore`
- Modify: `.github/workflows/ci.yml`
- Delete: tracked generated binaries or internal process files found by the hygiene check
- Create: `scripts/check-repository.ps1`

- [ ] Confirm the repository owner controls the existing MIT-licensed history; then replace `LICENSE` with the Apache License 2.0 text and use the verified copyright holder name.
- [ ] Update README license badge/link and contributor guidance to match Apache-2.0.
- [ ] Add a repository check that fails for tracked `.exe`, `.rewind`, coverage, local config, secrets, `docs/superpowers`, and stale AI/process filenames while allowing documented fixtures and source assets.
- [ ] Run the check before and after the cleanup and record the exact output.
- [ ] Update CI with a dedicated hygiene job that runs on pull requests before build/release jobs.
- [ ] Commit only this focused change with a factual message: `chore: establish repository hygiene and licensing`.

### Task 2: Frontend source and branded design system

**Files:**

- Create: `web/package.json`
- Create: `web/package-lock.json`
- Create: `web/index.html`
- Create: `web/src/main.ts`
- Create: `web/src/types.ts`
- Create: `web/src/styles/tokens.css`
- Create: `web/src/styles/base.css`
- Create: `web/src/styles/layout.css`
- Create: `web/src/styles/components.css`
- Create: `web/src/icons/index.ts`
- Create: `web/src/brand/rewind-mark.svg`
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`
- Modify: `.gitignore`
- Replace generated output: `internal/server/ui/dist/index.html`

- [ ] Add a deterministic Vite build that emits one self-contained `index.html` into `internal/server/ui/dist` and does not fetch runtime assets.
- [ ] Define tokens for canvas, surfaces, borders, text, semantic severity, spacing, radii, focus, and typography; use one restrained dark operations-console theme.
- [ ] Add the Rewind mark and a small inline SVG icon set with accessible labels and no emoji/text glyph substitutions.
- [ ] Build the shell with product header, incident context rail, content column, status bar, and responsive navigation.
- [ ] Preserve the current incident JSON contract and render all current evidence: verdicts, sources, anomalies, timeline, evidence details, entity lanes, scrubber, and raw excerpts.
- [ ] Add explicit loading, empty, no-trigger, partial-source, malformed-response, and fatal-error states.
- [ ] Add visible keyboard focus, semantic headings, `aria-live` for selection/error updates, reduced-motion support, and mobile layout behavior.
- [ ] Add `npm run typecheck`, `npm run build`, and a fixture-driven UI smoke test for API rendering and offline/no-network behavior.
- [ ] Run the new frontend checks and Go server tests before committing: `npm ci`, `npm run typecheck`, `npm run build`, `go test ./internal/server/...`.
- [ ] Commit with `feat: establish the Rewind incident workspace`.

### Task 3: Documentation and visual assets

**Files:**

- Modify: `README.md`
- Create: `docs/getting-started.md`
- Modify: `docs/architecture.md`
- Create: `docs/investigation-workflow.md`
- Modify: `docs/config-reference.md`
- Create: `docs/operations.md`
- Modify: `docs/bundle-spec.md`
- Modify: `CONTRIBUTING.md`
- Modify: `SECURITY.md`
- Create: `docs/assets/architecture.svg`
- Create: `docs/assets/investigation-flow.svg`
- Create: `docs/assets/ui-demo.svg`

- [ ] Rewrite the README around the product promise, screenshot/demo asset, quick start, real distributed-stack test, supported sources, limitations, architecture, security, and contribution links.
- [ ] Write task-oriented getting-started and investigation workflow guides using commands that exist in the CLI.
- [ ] Document the actual data flow, trust boundaries, offline replay guarantee, source partial-failure semantics, and rule limitations.
- [ ] Add source/config/operations details for credentials, sensitive bundles, CI exit codes, troubleshooting, and upgrades.
- [ ] Create repository-owned, secret-free SVG diagrams with legends and stable labels; do not claim customer adoption or fabricate metrics.
- [ ] Add Markdown-link checks and a documentation smoke check to CI.
- [ ] Run every documented quick-start command against the demo and practical fixture, then commit with `docs: make Rewind ready for public evaluation`.

### Task 4: Integrated verification and review artifacts

**Files:**

- Modify: `docs/verification/production-readiness-report.md`
- Modify: `docs/verification/ui-checklist.md`
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`

- [ ] Run `gofmt -l .`, `go vet ./...`, `go test -count=1 ./...`, lint, frontend typecheck/build, repository hygiene, and documentation checks.
- [ ] Build the CLI and execute every demo scenario; export, import, and replay at least one bundle with network access unavailable to the renderer.
- [ ] Run the persistent Docker practical stack in `D:\Coding Files\Projects\rewind-test`, inject the documented failure, collect a bundle, open it in the UI, and verify source health, critical evidence, and offline replay.
- [ ] Verify no browser console errors, broken icons, clipped narrow-layout content, missing focus state, or network requests occur in the offline bundle viewer when the browser test capability is unavailable; record the limitation rather than claiming a visual pass.
- [ ] Record exact commands, dates, commit, environment, pass/fail results, and known limitations in the verification report.
- [ ] Only after all evidence is collected, prepare a concise PR description with changed behavior, verification, and limitations; do not claim production readiness beyond the evidence.

