# UI verification checklist

- [x] Embedded asset is served by the Go server.
- [x] UI JavaScript passes node --check.
- [x] Dynamic values are rendered through text nodes/properties rather than
  interpolated HTML.
- [x] Keyboard-visible controls exist for the replay range and evidence
  timeline.
- [x] Responsive layout and reduced-motion CSS are present.
- [x] Empty, partial-source, and failed-source states have explicit copy.
- [ ] Browser screenshot verification: unavailable in this environment because
  the in-app browser had no active target (agent.browsers.list() returned an
  empty list). No visual pass is claimed.
