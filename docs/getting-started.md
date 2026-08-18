# Getting started

This path takes a new user from a local demo to a real, portable investigation.

## Install

From a machine with Go 1.25 or newer:

```bash
go install github.com/paultanay/rewind/cmd/rewind@latest
rewind version
```

For a source checkout:

```bash
git clone https://github.com/paultanay/rewind.git
cd rewind
npm --prefix web ci
make build
```

The source build embeds the frontend into the Go binary. `web/node_modules` and
local binaries are intentionally not part of the repository.

## Run the offline demo

```bash
rewind demo --scenario bad-deploy
rewind demo --scenario bad-deploy --ui --port 7750
```

The demo exercises a deployment followed by latency and error change-points.
Open the printed local URL and inspect the assessment, source coverage,
timeline, and evidence panel.

Available scenarios are `bad-deploy`, `oom-cascade`, `node-pressure`,
`cpu-throttle`, and `false-positive`.

## Save a portable investigation

```bash
rewind demo --scenario bad-deploy -o incident.rewind
rewind import incident.rewind
rewind investigate --replay incident.rewind --format json
rewind ui incident.rewind
```

The replay command reads the bundle fixtures rather than querying live systems.
Treat the resulting file as sensitive operational data.

## Connect existing systems

Copy `rewind.yaml.example` to `rewind.yaml`, set the source URLs and credentials,
then check connectivity before investigating:

```bash
rewind sources --config rewind.yaml
rewind investigate --config rewind.yaml --from -45m --namespace shop -o incident.rewind
rewind ui incident.rewind
```

Read [Configuration reference](config-reference.md) for all fields and
[Source guides](sources/) for prerequisites and query behaviour.

## What to verify before sharing a result

- the time window includes the suspected change and impact;
- source health is not hiding a partial or failed collector;
- the ranked hypothesis has a supporting chain rather than an alert alone;
- the bundle contains no credentials or unbounded raw data; and
- the limitations are included in the postmortem.
