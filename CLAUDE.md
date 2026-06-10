# CLAUDE.md — gpufleet-cli (module session rules)

You are a Claude session **scoped to this repo only** (`gpufleet-cli`). This is
an OPEN module (Apache-2.0). Your edits are **confined to this repo**.

## What this module is

The local CLI/TUI that wires `gpufleet-agent` + `gpufleet-semantics` +
`gpufleet-rca` together to render a job-level utilization/cost view. Standalone
with NO control plane.

## Hard boundaries (do not cross)

- **NO closed logic.** Never import, vendor, or reimplement control-plane logic
  (deep playbooks, the gate service, LLM narration, learned baselines). This
  binary must be fully useful with no control plane.
- **No LLM here.** No Claude/Anthropic API call in this repo.
- **Edits confined here.** Need a change in `semantics`, `agent`, `rca`, or the
  control plane? ABSTAIN and file a blocker. Do not reach across — do not edit a
  sibling repo even though `replace` points at it locally.
- **`proto/` is READ-ONLY.** Read vendored contracts; never edit them. A needed
  contract change = a *contract change proposal* blocker for the orchestrator.
- **Determinism-first.** Rendering must be reproducible (sorted output, no
  wall-clock in output).
- **No externally-sourced content.**

## If you are blocked

File a short blocker and stop. No cross-repo workarounds.

## Definition of done

`go test -race ./...` and `go vet ./...` pass.
