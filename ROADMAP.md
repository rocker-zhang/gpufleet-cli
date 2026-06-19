# cli — module roadmap (ROADMAP.md)

Module-local milestone breakdown for the OPEN `cli` (read-only bypass viewer).
Mirrors `../ROADMAP.md` / `../ops/PLAN.md`. cli is **bypass**: it NEVER uploads,
NEVER originates evidence-pack egress, holds NO license logic, contains NO closed
logic (D-0008, D-0010). It renders two read-only views: Endpoint 1 (local agent,
open, single-node) and Endpoint 2 (controlplane, paid, fleet).

| Milestone | cli delivers | Exit criteria |
|---|---|---|
| **M1** — 契约 + 骨架 | Consume `proto` at the pinned tag (open `Verdict` + the small aggregation envelope) read-only. Viewer skeleton + deterministic render plumbing. | Builds against pinned `proto`; `go test -race ./...` + `go vet ./...` green; no closed deps; no `proto` edits. |
| **M2** — wedge render (money story) | Endpoint 1: HTTP-GET device-level utilization + $cost (MFU, tensor-active, $waste, low_util) from the agent local API (**localhost HTTP**: `/signals` protojson `gpufleet.v1.EvidencePack` + `/cost`) and render. Direct mode (linking `semantics`) is deferred to a later card. | `cli view --endpoint` renders per-device MFU + $cost from the agent local API (per-job rollup only when the agent has a job-label source; vanilla DCGM has none → device-level), **standalone, no control plane**; output deterministic (sorted, no wall-clock); cli running/not-running does not affect the agent→controlplane path (proves bypass). |
| **M3** — Verdict + ABSTAIN render | Render the deterministic RCA Verdict and ABSTAIN state surfaced by the agent local API (fault_class \| ABSTAIN, cited_signals, confidence). cli only displays; it never adjudicates. | Injected-fault case renders correct fire/ABSTAIN as reported by the agent; no gate logic in cli. |
| **M4** — public scorecard view | Render the public benchmark scorecard (questions/results) — view only. No answer key, no corpus, no closed logic. | Scorecard renders from open data; reproducible output; zero closed/eval-corpus dependency. |
| **M5** — Endpoint 2 (paid fleet, read-only) | Add the controlplane fleet endpoint: cross-node aggregation, fleet cost attribution, deep verdict — over the **open thin wire contract** only. License enforced **server-side**; cli holds none. cli still never uploads. | cli reads a fleet view from the controlplane and renders it; with no license the server returns nothing and cli degrades gracefully (still fully usable on Endpoint 1); no license code, no egress origination in cli. |
| **M6** — partner polish | Fleet UX hardening for 1–2 GPU validations: cross-node cost/verdict views, clean degrade between Endpoint 1 (open) and Endpoint 2 (paid). cli stays fully open & "dumb". | Partner can run single-node free and fleet paid views; cli remains open, no closed logic, no upload path. |

## Invariants across all milestones

- **D-0010**: Endpoint 1 = local agent localhost HTTP = single-node = OPEN/free;
  Endpoint 2 = controlplane fleet API = cross-node + fleet cost + deep verdict =
  CLOSED/PAID (买点). License is server-side only.
- **D-0008**: cli never UPLOADS / never originates evidence-pack egress. Reading a
  fleet view from the controlplane is the paid *read* feature; the agent remains
  the sole HTTPS egress and sole evidence-pack assembler.
- Open thin wire contract only (open `Verdict` + aggregation envelope in `proto`);
  closed implementation + data + license stay server-side. cli stays open & dumb.
- Read-only, deterministic rendering; no LLM; no closed logic; `proto` read-only.
