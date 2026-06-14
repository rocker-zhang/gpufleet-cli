# gpufleet-cli

Apache-2.0 · OPEN module · `github.com/rocker-zhang/gpufleet-cli`

A read-only **bypass viewer**. It HTTP-GETs the agent's local read-only API
(`/signals` — canonical protojson of `gpufleet.v1.EvidencePack` — `/cost`, and
`/verdict` — canonical protojson of `gpufleet.v1.Verdict`) and renders a
deterministic **single-node utilization/cost view + RCA verdict banner**. It is
off the critical path: it assembles no evidence pack, originates no egress,
contacts no control plane, and never writes back.

It is **standalone-useful with no control plane** and contains **no closed
logic**. The closed control plane (deep playbooks, the gate service, LLM
narration) is never imported here. The window-level RCA verdict is the agent's
local open-gate Verdict, rendered as a banner **verbatim** — cli has no gate of
its own and never recomputes, judges, or fabricates a fault class. When the agent
has no verdict yet (pre-window or unreachable), the banner reads
`RCA: (no verdict yet)`. Deep RCA + narration remain the closed control plane.

## Use

Start the agent's local read-only API, then point cli at it:

```sh
agent -serve -addr 127.0.0.1:9577        # the agent (separate module)
go run ./cmd/gpufleet view --endpoint http://127.0.0.1:9577
```

`--endpoint` defaults to `http://127.0.0.1:9577`. The `mfu` column is **per-device**;
on a box exposing only tensor-active (no directly measured achieved-FLOPs) it is a
**tensor-active-derived estimate** (duty-cycle proxy), not a true achieved-FLOP value.
The `JOBS` section is populated only when the agent has a **job-label source**
(scheduler / Prometheus relabel); vanilla DCGM has no job label, so jobs are null
and the view is device-level. Output (deterministic, sorted):

```
gpufleet single-node view  (agent=agent-test, source=local read-only API)

DEVICES
  device         node         mfu  tensor  waste(win)     $/hr  lowutil
  GPU-healthy    n1         0.620   0.700     $0.0000  $0.0000  -
  GPU-idle       n1         0.010   0.020     $0.0190  $1.1400  LOW

JOBS
  job    waste(win)     $/hr  priced  devices
  job-a     $0.0190  $1.1400  yes           2

RCA VERDICT  (window-level, from the agent's local open gate)
RCA: GPU_FALLEN_OFF_BUS  confidence 0.95  signature GATE_SIGNATURE_XID79_FALLEN_OFF_BUS
cited signals (2):
  - device.lost.dcgm.GPU-mock-0001 @ DCGM  (DCGM health: device unreachable on the bus)
  - dmesg.xid79.GPU-mock-0001 @ DMESG_XID  (NVRM Xid 79 (GPU fallen off the bus))
note: deep RCA + narration is the closed control plane; the open verdict is class + cited signals + confidence only (no narration).
```

When the open ≥2-independent-signal gate does not corroborate a fault class the
banner instead reads `RCA: ABSTAIN — …` (the honest safe default), and before the
first window / when the agent is unreachable it reads `RCA: (no verdict yet)`.

An end-to-end M3 demo (FIRE + ABSTAIN) lives in [`demo/m3-demo.sh`](demo/m3-demo.sh).

## Poly-repo note

In CI the read-only `proto` gen module is consumed at its pinned tag
(`proto/v0.1.0`). For local development the `go.mod` uses a single `replace`
pointing at `../proto/gen/go`; override with a `go.work` (or drop the replace) to
build against the tagged release. cli links **no** other sibling module — it
reads the agent over HTTP, not as a Go dependency.

## Boundaries

- No closed logic, no control-plane dependency, no LLM call.
- `proto/` is a read-only dependency; this repo never edits it.

## Develop

```sh
go test -race ./...
go vet ./...
```

CI: lint, arm64 test matrix with `-race`, static cross builds (amd64+arm64),
govulncheck, syft SBOM, gitleaks. Releases via GoReleaser + cosign keyless +
SLSA provenance + SBOM on `v*` tags.
