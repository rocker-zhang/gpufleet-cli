# gpufleet-cli

Apache-2.0 · OPEN module · `github.com/rocker-zhang/gpufleet-cli`

A read-only **bypass viewer**. It HTTP-GETs the agent's local read-only API
(`/signals` — canonical protojson of `gpufleet.v1.EvidencePack` — and `/cost`)
and renders a deterministic **single-node utilization/cost view**. It is off the
critical path: it assembles no evidence pack, originates no egress, contacts no
control plane, and never writes back.

It is **standalone-useful with no control plane** and contains **no closed
logic**. The closed control plane (deep playbooks, the gate service, LLM
narration) is never imported here. RCA/Verdict is M3+, so the verdict column
renders `n/a (no control plane)` — never fabricated.

## Use

Start the agent's local read-only API, then point cli at it:

```sh
agent -serve -addr 127.0.0.1:9577        # the agent (separate module)
go run ./cmd/gpufleet view --endpoint http://127.0.0.1:9577
```

`--endpoint` defaults to `http://127.0.0.1:9577`. Output (deterministic, sorted):

```
gpufleet single-node view  (agent=agent-test, source=local read-only API)

DEVICES
  device               node          mfu   tensor   waste(win)  lowutil  verdict
  GPU-healthy          n1          0.620    0.700      $0.0000        -  n/a (no control plane)
  GPU-idle             n1          0.010    0.020      $0.0190      LOW  n/a (no control plane)

JOBS
  job                    waste(win)   priced  devices
  job-a                     $0.0190      yes        2
```

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
