# cli — module brief (CLAUDE.md)

## 1. 身份

- **class**: OPEN (Apache-2.0)
- **language**: Go
- **kind**: viewer (CLI/TUI)
- **purpose (one line)**: a read-only bypass viewer that renders job-level
  utilization / $cost / Verdict — single-node from the agent's local API (open),
  fleet from the controlplane (paid).
- **on-path | bypass | shared-lib**: **bypass**. cli is NEVER on the data-plane
  upload path. It originates no evidence-pack egress. The agent uploads; cli only
  reads and renders.

## 2. 在系统里的位置

cli is the human window onto everything else. It **produces** nothing into the
system — no telemetry, no evidence pack, no upload. It only **consumes** views
and renders them deterministically.

- **Consumes (Endpoint 1 — local agent API, OPEN/free, single-node)**: the
  agent's local read-only API over **localhost HTTP** (what the agent actually
  serves: `agent -serve -addr 127.0.0.1:9577`; a unix socket may come later).
  cli HTTP-GETs `/signals` (canonical protojson of `gpufleet.v1.EvidencePack`) +
  `/cost` (the standalone cost wedge) and renders the cost/MFU view for ONE node.
  This is the standalone, no-control-plane path (the wedge demo). Verdict/RCA is
  M3+ — not served yet, so cli renders `n/a (no control plane)`, never fabricated.
- **Consumes (Endpoint 2 — controlplane fleet API, CLOSED/PAID, fleet)**: the
  controlplane's fleet API for cross-node aggregation, fleet cost attribution,
  and the deep verdict. cli speaks only the **open thin wire contract** — the
  open `Verdict` + a small aggregation envelope defined in open `proto`. The
  closed implementation, data, and license gate all live server-side; cli stays
  fully open and "dumb".
- **Direct mode (out of scope this milestone)**: a possible future card may link
  `semantics` for an agent-less one-shot glance. TASK-0020 does **not** do this:
  cli reads the agent over HTTP and links NO sibling Go module other than the
  read-only `proto` gen contract (no agent, no rca, no semantics — TASK-0016).
- **Edges**: cli → agent local API (localhost HTTP, read); cli → controlplane
  fleet API (read, paid). No other edges. See `../ARCHITECTURE.md` and D-0008 / D-0010.

## 3. 继承的红线

Inherits `../RULES.md` in full. Module-specific hard lines:

- **Read-only bypass viewer.** NEVER uploads, NEVER originates evidence-pack
  egress. Reading a fleet view from the controlplane is a *read*; the agent is
  the sole HTTPS egress and sole evidence-pack assembler (D-0008 invariant holds).
- **Two endpoints (D-0010)**: Endpoint 1 = local agent API (localhost HTTP) =
  single-node = OPEN/free. Endpoint 2 = controlplane fleet API = cross-node
  aggregation + fleet cost + deep verdict = CLOSED/PAID (the selling point / 买点).
- **No license logic in cli.** License is enforced SERVER-SIDE at the
  controlplane, never in the open cli. cli just makes a read request; the server
  decides what to return.
- **No closed logic.** Never import, vendor, or reimplement controlplane internals
  (deep playbooks, the gate service, LLM narration, learned baselines). cli must
  be fully useful with Endpoint 1 alone and no control plane.
- **No LLM here.** No Claude/Anthropic API call in this repo. cli renders the
  narration string the server already produced; it never generates it.
- **`proto/` is READ-ONLY.** A needed contract change = a contract-change proposal
  blocker for the orchestrator. Never edit vendored contracts.
- **Determinism-first.** Rendering is reproducible — sorted output, no wall-clock
  in rendered values.
- **No externally-sourced content.**

## 4. 当前任务 & 里程碑焦点

- Board cards: **TASK-0020** (cli → pure bypass viewer of the agent local API,
  supersedes TASK-0008) and **TASK-0016** (cli cross-module wiring / CI). See
  `../ops/BOARD.md`.
- **TASK-0020 governs**: cli reads the agent local API and renders the TUI; it
  ASSEMBLES NO pack, sends NO HTTPS, receives NO controlplane Verdict on the data
  plane. D-0010 amends this with the second (paid, read-only) fleet endpoint.
- **Current milestone (M2)**: end-to-end render of job-level utilization + $cost
  attribution from the agent local API, standalone with no control plane (the
  money story / demo1).

## 5. 构建与测试

```sh
source ../.envrc          # project-local toolchain (./.tools, ./.cache) — REQUIRED first
go build ./...
go test -race ./...
go vet ./...
```

- **DoD**: `go test -race ./...` and `go vet ./...` pass.
- **CI**: build + race test + vet on the open repo; sibling `gpufleet-*` modules
  consumed at pinned tags in CI (local dev uses `replace` / `go.work`).

## 6. session 工作规则

- **Edits confined to this repo** (`cli/`). Do not edit a sibling repo even if a
  local `replace` points at it.
- **`proto/` read-only.** Contract change needed → ABSTAIN + file a contract-change
  proposal blocker for the orchestrator.
- **ABSTAIN + report** if the task needs a change in `agent`, `semantics`, `rca`,
  `proto`, or the controlplane — file a short blocker and stop. No cross-repo
  workarounds.
- **Provenance**: personal hardware/time only; no externally-sourced code, data, or
  error-code semantics.

## 7. 模块路线图 (mirror ROADMAP.md)

- **M1** — consume `proto` (open Verdict + aggregation envelope, read-only); skeleton viewer.
- **M2** — render job-level MFU + $cost from the agent local API (Endpoint 1), standalone, no control plane. (money story)
- **M3** — render deterministic RCA Verdict + ABSTAIN from the agent local API.
- **M4** — render the public scorecard view; no answer-key, no closed logic.
- **M5** — Endpoint 2: read-only fleet view from the controlplane (paid, server-side license); render cross-node cost + deep verdict.
- **M6** — partner polish: fleet UX for real GPU validation; cli stays open & dumb.
