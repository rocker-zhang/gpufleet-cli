package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gpufleetv1 "github.com/rocker-zhang/gpufleet-proto/gen/go/gpufleet/v1"
)

// staleAgentStub stands up an httptest agent whose /cost reports the TASK-0040
// freshness fields stale=true with an age past the threshold and a reason. cli
// must render the data-age line, a prominent STALE marker, and the agent's
// reason — passing the verdict through, never recomputing it.
func staleAgentStub(t *testing.T) *httptest.Server {
	t.Helper()
	signals := realSignalsProtoJSON(t)
	costJSON := `{
	  "devices": [
	    {"uuid":"GPU-idle","node":"n1","mfu":0.01,"tensor_active":0.02,"idle_fraction":0.95,"cost_usd":0.02,"wasted_usd":0.019,"usd_per_hour":1.14,"priced":true,"low_utilization":true}
	  ],
	  "jobs": [
	    {"job_id":"job-a","wasted_usd":0.019,"usd_per_hour":1.14,"priced":true,"devices":1}
	  ],
	  "collected_at":"2026-01-02T03:04:05Z",
	  "age_seconds": 42.5,
	  "stale": true,
	  "stale_reason": "3 consecutive collection failure(s): exporter unreachable"
	}`
	mux := http.NewServeMux()
	mux.HandleFunc("/signals", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(signals))
	})
	mux.HandleFunc("/cost", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(costJSON))
	})
	return httptest.NewServer(mux)
}

// TestRenderStaleMarker is the cli half of the TASK-0040 DoD: on a stale /cost,
// the render shows the data age, a prominent STALE marker, the agent's reason,
// and a clear note — while STILL rendering the last-known device values (held,
// not blanked). cli passes the agent's verdict through verbatim.
func TestRenderStaleMarker(t *testing.T) {
	srv := staleAgentStub(t)
	defer srv.Close()

	c := NewClient(srv.URL)
	ctx := context.Background()
	pack, err := c.Signals(ctx)
	if err != nil {
		t.Fatalf("Signals: %v", err)
	}
	cost, err := c.Cost(ctx)
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	if !cost.Stale {
		t.Fatalf("stub /cost should decode stale=true into cli DTO")
	}

	out := RenderView(pack, cost)

	// Data-age line present with the agent-reported age.
	if !strings.Contains(out, "data age: 42.5s") {
		t.Errorf("expected data-age line with 42.5s:\n%s", out)
	}
	// Prominent STALE marker.
	if !strings.Contains(out, "STALE") {
		t.Errorf("expected a prominent STALE marker:\n%s", out)
	}
	// The agent's provenance reason is surfaced (not silently dropped).
	if !strings.Contains(out, "exporter unreachable") {
		t.Errorf("expected the agent's stale reason to be surfaced:\n%s", out)
	}
	// A clear note that the data is not current.
	if !strings.Contains(out, "do not treat as current") {
		t.Errorf("expected a clear not-current note on stale:\n%s", out)
	}
	// Last-known device value is STILL rendered (held, not blanked).
	if !strings.Contains(out, "GPU-idle") {
		t.Errorf("stale render must KEEP the last-known device values:\n%s", out)
	}
}

// TestRenderFreshNoStaleMarker proves the backward-compatible path: a FRESH
// /cost (stale=false) renders a data-age line but NO STALE marker and NO
// not-current note, so nothing changes for the always-fresh mock default beyond
// the new age line.
func TestRenderFreshNoStaleMarker(t *testing.T) {
	srv := agentStub(t) // the existing fresh stub: no freshness fields ⇒ stale=false, age 0
	defer srv.Close()

	c := NewClient(srv.URL)
	ctx := context.Background()
	pack, _ := c.Signals(ctx)
	cost, _ := c.Cost(ctx)

	if cost.Stale {
		t.Fatalf("fresh stub must decode stale=false")
	}
	out := RenderView(pack, cost)
	if strings.Contains(out, "STALE") {
		t.Errorf("fresh render must NOT show a STALE marker:\n%s", out)
	}
	if strings.Contains(out, "do not treat as current") {
		t.Errorf("fresh render must NOT show the not-current note:\n%s", out)
	}
	if !strings.Contains(out, "data age:") {
		t.Errorf("fresh render should still show a data-age line:\n%s", out)
	}
	// Sanity: the fresh stub omits freshness fields, so age renders as 0.0s.
	if !strings.Contains(out, "data age: 0.0s") {
		t.Errorf("fresh stub (no freshness fields) should render age 0.0s:\n%s", out)
	}
}

// TestStaleRenderDeterministic locks determinism for the stale render too (same
// input ⇒ byte-identical output; no cli wall-clock).
func TestStaleRenderDeterministic(t *testing.T) {
	srv := staleAgentStub(t)
	defer srv.Close()
	c := NewClient(srv.URL)
	ctx := context.Background()
	pack, _ := c.Signals(ctx)
	cost, _ := c.Cost(ctx)
	var _ *gpufleetv1.EvidencePack = pack
	if a, b := RenderView(pack, cost), RenderView(pack, cost); a != b {
		t.Errorf("stale render not deterministic")
	}
}

// neverCollectedAgentStub mimics the TASK-0041 agent: /cost returns 200 with
// empty devices, stale=true, never_collected=true, and a reason (the exporter was
// unreachable from startup). /signals returns 503 like the real agent before any
// window exists — Client.Signals surfaces that error, but the view command reads
// /cost too, and cli must render a clear message off /cost.
func neverCollectedAgentStub(t *testing.T) *httptest.Server {
	t.Helper()
	costJSON := `{
	  "devices": [],
	  "jobs": [],
	  "age_seconds": 9.0,
	  "stale": true,
	  "never_collected": true,
	  "stale_reason": "never collected: no successful metrics scrape since startup (1 consecutive metrics-collection failure(s): connection refused)"
	}`
	mux := http.NewServeMux()
	mux.HandleFunc("/signals", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no signal window collected yet", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/cost", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(costJSON))
	})
	return httptest.NewServer(mux)
}

// TestRenderNeverCollectedGraceful is the TASK-0041 cli DoD: on a never-collected
// agent (200 /cost with empty devices + never_collected=true), RenderView prints a
// CLEAR human message carrying the agent's reason — never a blank table, never a
// raw HTTP error.
func TestRenderNeverCollectedGraceful(t *testing.T) {
	srv := neverCollectedAgentStub(t)
	defer srv.Close()

	c := NewClient(srv.URL)
	cost, err := c.Cost(context.Background())
	if err != nil {
		t.Fatalf("Cost on a never-collected (200) agent must not error: %v", err)
	}
	if !cost.NeverCollected || !cost.Stale {
		t.Fatalf("never-collected /cost should decode never_collected+stale: %+v", cost)
	}

	out := RenderView(nil, cost) // /signals was 503, so pack may be nil — must not panic
	if !strings.Contains(out, "NO DATA") {
		t.Errorf("expected a clear NO DATA message:\n%s", out)
	}
	if !strings.Contains(out, "has not collected") {
		t.Errorf("expected a human 'has not collected' message:\n%s", out)
	}
	if !strings.Contains(out, "never collected") {
		t.Errorf("expected the agent's reason to be surfaced:\n%s", out)
	}
	// Never a raw HTTP error / never a blank device table.
	if strings.Contains(out, "status 5") || strings.Contains(out, "DEVICES\n") {
		t.Errorf("never-collected render must not show a raw HTTP error or a blank device table:\n%s", out)
	}
}

// TestCostGracefulOn503 proves the defensive path for an OLDER agent that still
// returns 503 "no signal window collected yet" on /cost (pre-TASK-0041 behavior):
// Client.Cost must degrade it to a NeverCollected empty state carrying the agent's
// body as the reason, NOT bubble a raw HTTP status error.
func TestCostGracefulOn503(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/cost", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no signal window collected yet", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cost, err := NewClient(srv.URL).Cost(context.Background())
	if err != nil {
		t.Fatalf("a 503 /cost must degrade gracefully, not error: %v", err)
	}
	if !cost.NeverCollected || !cost.Stale {
		t.Fatalf("503 /cost must become a never-collected empty state: %+v", cost)
	}
	if !strings.Contains(cost.StaleReason, "no signal window collected yet") {
		t.Fatalf("503 reason should carry the agent's message: %q", cost.StaleReason)
	}
	out := RenderView(nil, cost)
	if !strings.Contains(out, "NO DATA") || strings.Contains(out, "status 503") {
		t.Errorf("503 must render a clear message, not a raw HTTP error:\n%s", out)
	}
}

// TestCostTransportErrorStillErrors proves we did NOT over-swallow: when the agent
// is unreachable (connection refused — a transport error, not a non-200), Cost
// still returns an error so the caller can say "cannot reach the agent". A
// never-collected agent (reachable, 200/503) is distinct from a down agent.
func TestCostTransportErrorStillErrors(t *testing.T) {
	// An unroutable endpoint: nothing listening.
	c := NewClient("http://127.0.0.1:0")
	if _, err := c.Cost(context.Background()); err == nil {
		t.Fatalf("a transport failure (agent down) must still surface an error")
	}
}
