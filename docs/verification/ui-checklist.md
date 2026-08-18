# UI verification checklist

- [x] Embedded asset is served by the Go server.
- [x] TypeScript source passes the frontend typecheck.
- [x] Vite produces the embedded UI output and the fixture smoke test passes.
- [x] Dynamic values are rendered through text nodes/properties rather than
  interpolated HTML.
- [x] Keyboard-visible controls, semantic landmarks, and visible focus styles
  exist for the replay range and evidence timeline.
- [x] Responsive layout and reduced-motion CSS are present.
- [x] Empty, partial-source, and failed-source states have explicit copy.
- [x] Manual practical-validation screenshot is committed at
  `docs/assets/ui-demo.png` and contains synthetic demo data only.
- [ ] Automated browser interaction verification: not run in this environment.
