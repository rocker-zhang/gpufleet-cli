#!/usr/bin/env bash
# m3-demo.sh — end-to-end M3 demo for the gpufleet OPEN single-node path
# (TASK-0049). Stands up the agent's local read-only API and renders the cli
# view twice: once with an injected 2-source fault (the gate FIRES) and once
# with the default mock (the gate ABSTAINs). No control plane, no egress — this
# is the standalone open path (D-0010 Endpoint 1).
#
# Lives in the cli repo (cli/demo/) so it is self-contained; the orchestrator may
# wire it into ops/ if desired. It builds both binaries from the local workspace.
#
# Usage:
#   ./demo/m3-demo.sh                 # build agent+cli from the workspace, run both scenarios
#   AGENT_BIN=/path CLI_BIN=/path \
#     ./demo/m3-demo.sh               # use prebuilt binaries instead of building
#
# Requires: a project-local Go toolchain (run `source ../.envrc` first if building).

set -euo pipefail

CLI_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKSPACE_DIR="$(cd "${CLI_DIR}/.." && pwd)"
AGENT_DIR="${WORKSPACE_DIR}/agent"

ADDR_FIRE="127.0.0.1:9577"
ADDR_ABSTAIN="127.0.0.1:9578"

AGENT_BIN="${AGENT_BIN:-}"
CLI_BIN="${CLI_BIN:-}"

build_if_needed() {
  if [[ -z "${AGENT_BIN}" ]]; then
    AGENT_BIN="$(mktemp -t gpufleet-agent.XXXXXX)"
    echo "# building agent -> ${AGENT_BIN}"
    ( cd "${AGENT_DIR}" && go build -o "${AGENT_BIN}" ./cmd/agent )
  fi
  if [[ -z "${CLI_BIN}" ]]; then
    CLI_BIN="$(mktemp -t gpufleet-cli.XXXXXX)"
    echo "# building cli -> ${CLI_BIN}"
    ( cd "${CLI_DIR}" && go build -o "${CLI_BIN}" ./cmd/gpufleet )
  fi
}

# wait_for_verdict <endpoint> — poll the cli view until the agent has published a
# window (the RCA banner shows a real verdict line), or give up after ~20s.
wait_for_verdict() {
  local endpoint="$1" i
  for i in $(seq 1 20); do
    if "${CLI_BIN}" view --endpoint "${endpoint}" 2>/dev/null | grep -q "RCA: "; then
      return 0
    fi
    sleep 1
  done
  echo "!! agent at ${endpoint} did not publish a window in time" >&2
  return 1
}

scenario() {
  local title="$1" addr="$2"; shift 2
  local logf; logf="$(mktemp -t gpufleet-agent-log.XXXXXX)"

  echo
  echo "==================================================================="
  echo "  ${title}"
  echo "  agent: GPUFLEET_COLLECTORS=mock ${AGENT_BIN} -serve -addr ${addr} $*"
  echo "==================================================================="

  GPUFLEET_COLLECTORS=mock "${AGENT_BIN}" -serve -addr "${addr}" -interval 2s "$@" >"${logf}" 2>&1 &
  local pid=$!
  # shellcheck disable=SC2064
  trap "kill ${pid} 2>/dev/null || true" RETURN

  if ! wait_for_verdict "http://${addr}"; then
    cat "${logf}" >&2
    kill "${pid}" 2>/dev/null || true
    return 1
  fi

  echo "----- cli view -----"
  "${CLI_BIN}" view --endpoint "http://${addr}"

  echo "----- POST /verdict must be 405 (read-only, RULES A) -----"
  local code
  code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://${addr}/verdict" || true)"
  echo "POST /verdict -> ${code}"

  kill "${pid}" 2>/dev/null || true
  wait "${pid}" 2>/dev/null || true
  trap - RETURN
}

build_if_needed
scenario "M3 DEMO 1/2 — FIRE (--inject-faults: 2-source ECC+XID79 pattern)" "${ADDR_FIRE}" -inject-faults
scenario "M3 DEMO 2/2 — ABSTAIN (default mock, no fault)" "${ADDR_ABSTAIN}"

echo
echo "# done. FIRE -> RCA: GPU_FALLEN_OFF_BUS with 2 distinct-source cites; ABSTAIN -> RCA: ABSTAIN."
