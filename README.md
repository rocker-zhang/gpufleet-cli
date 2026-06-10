# gpufleet-cli

Apache-2.0 · OPEN module · `github.com/rocker-zhang/gpufleet-cli`

The local CLI/TUI. It wires the open libraries — `gpufleet-agent` (collector),
`gpufleet-semantics` (cost/efficiency math), and `gpufleet-rca` (deterministic
RCA) — together on your machine to print a **job-level utilization/cost view**.

It is **standalone-useful with no control plane** and contains **no closed
logic**. The closed control plane (deep playbooks, the gate service, LLM
narration) is never imported here.

## Use

```sh
go run ./cmd/gpufleet view --node my-host --job training-run-7
```

Output (mock source by default — no GPU needed):

```
job training-run-7  (source=mock)
  mean MFU:       0.560
  straggler:      0.500
  cost (window):  $0.0400
  devices:
    GPU-mock-0001    mfu=0.080 tensor=0.700 $0.0200
    GPU-mock-0002    mfu=0.040 tensor=0.333 $0.0200
```

## Poly-repo note

In CI the sibling `gpufleet-*` modules are consumed at pinned tags. For local
development the `go.mod` uses `replace` directives pointing at the sibling repos;
override with a `go.work` (or drop the replaces) to build against tagged
releases.

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
