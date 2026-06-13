package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gpufleetv1 "github.com/rocker-zhang/gpufleet-proto/gen/go/gpufleet/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// realSignalsProtoJSON builds a REAL gpufleet.v1.EvidencePack (the generated
// gen type, not a mirror) with two device->job mappings, then marshals it with
// protojson exactly as the agent's /signals serves it. This proves cli parses
// the canonical wire shape with the real gen type.
func realSignalsProtoJSON(t *testing.T) string {
	t.Helper()
	pack := &gpufleetv1.EvidencePack{
		ContractVersion: "v1",
		AgentId:         "agent-test",
		Mappings: []*gpufleetv1.DeviceJobMapping{
			{DeviceUuid: "GPU-healthy", Node: "n1", JobId: "job-a", PeakTflops: 125, CostRateUsdPerHour: 1.20},
			{DeviceUuid: "GPU-idle", Node: "n1", JobId: "job-a", PeakTflops: 125, CostRateUsdPerHour: 1.20},
		},
		Provenance: map[string]string{"exporter": "mock"},
	}
	b, err := protojson.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal EvidencePack: %v", err)
	}
	return string(b)
}

// agentStub stands up an httptest server that mimics the agent's local
// read-only API: /signals (real protojson EvidencePack) + /cost (cost JSON).
// healthy device => wasted $0; idle device => wasted >$0 + low_utilization.
func agentStub(t *testing.T) *httptest.Server {
	t.Helper()
	signals := realSignalsProtoJSON(t)
	costJSON := `{
	  "devices": [
	    {"uuid":"GPU-healthy","node":"n1","mfu":0.62,"tensor_active":0.70,"idle_fraction":0.05,"cost_usd":0.0200,"wasted_usd":0.0,"usd_per_hour":0.0,"priced":true,"low_utilization":false},
	    {"uuid":"GPU-idle","node":"n1","mfu":0.01,"tensor_active":0.02,"idle_fraction":0.95,"cost_usd":0.0200,"wasted_usd":0.0190,"usd_per_hour":1.1400,"priced":true,"low_utilization":true}
	  ],
	  "jobs": [
	    {"job_id":"job-a","wasted_usd":0.0190,"usd_per_hour":1.1400,"priced":true,"devices":2}
	  ]
	}`
	mux := http.NewServeMux()
	mux.HandleFunc("/signals", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "read-only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(signals))
	})
	mux.HandleFunc("/cost", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "read-only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(costJSON))
	})
	return httptest.NewServer(mux)
}

// TestViewEndToEnd is the DoD end-to-end: stand up a stub agent API, GET
// /signals (parsed via the REAL gen type) + /cost, render, and assert the
// healthy device shows $0 while the idle device shows >$0 with low_util — all
// purely reading, no write-back.
func TestViewEndToEnd(t *testing.T) {
	srv := agentStub(t)
	defer srv.Close()

	c := NewClient(srv.URL)
	ctx := context.Background()

	pack, err := c.Signals(ctx)
	if err != nil {
		t.Fatalf("Signals: %v", err)
	}
	// Proof it is the real gen type: typed proto getters work.
	if pack.GetContractVersion() != "v1" || pack.GetAgentId() != "agent-test" {
		t.Fatalf("EvidencePack not parsed via gen type: %+v", pack)
	}
	if got := len(pack.GetMappings()); got != 2 {
		t.Fatalf("want 2 mappings, got %d", got)
	}

	cost, err := c.Cost(ctx)
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}

	out := RenderView(pack, cost)

	// Healthy device: wasted $0.0000, not LOW.
	healthyLine := lineContaining(out, "GPU-healthy")
	if healthyLine == "" {
		t.Fatalf("no GPU-healthy line:\n%s", out)
	}
	if !strings.Contains(healthyLine, "$0.0000") {
		t.Errorf("healthy device should show $0 wasted:\n%s", healthyLine)
	}
	if strings.Contains(healthyLine, "LOW") {
		t.Errorf("healthy device should not be LOW:\n%s", healthyLine)
	}

	// Idle device: wasted >$0 and LOW flag.
	idleLine := lineContaining(out, "GPU-idle")
	if idleLine == "" {
		t.Fatalf("no GPU-idle line:\n%s", out)
	}
	if strings.Contains(idleLine, "$0.0000") {
		t.Errorf("idle device should show wasted >$0:\n%s", idleLine)
	}
	if !strings.Contains(idleLine, "LOW") {
		t.Errorf("idle device should be flagged LOW:\n%s", idleLine)
	}

	// Verdict column is fixed n/a — never fabricated.
	if !strings.Contains(out, "n/a (no control plane)") {
		t.Errorf("verdict must be n/a (no control plane):\n%s", out)
	}

	// Deterministic order: GPU-healthy sorts before GPU-idle.
	if strings.Index(out, "GPU-healthy") > strings.Index(out, "GPU-idle") {
		t.Errorf("devices not in deterministic UUID order:\n%s", out)
	}
}

// TestViewDeterministic asserts byte-identical render for identical input.
func TestViewDeterministic(t *testing.T) {
	srv := agentStub(t)
	defer srv.Close()
	c := NewClient(srv.URL)
	ctx := context.Background()
	pack, _ := c.Signals(ctx)
	cost, _ := c.Cost(ctx)
	a := RenderView(pack, cost)
	b := RenderView(pack, cost)
	if a != b {
		t.Errorf("render not deterministic")
	}
}

// TestDegradeMarksPassThrough proves missing-field marks come only from agent
// state: an unpriced device yields a "cost" mark; a mapped-but-omitted device
// yields an "mfu" mark. cli fabricates nothing.
func TestDegradeMarksPassThrough(t *testing.T) {
	pack := &gpufleetv1.EvidencePack{
		AgentId: "a",
		Mappings: []*gpufleetv1.DeviceJobMapping{
			{DeviceUuid: "GPU-priced", JobId: "j"},
			{DeviceUuid: "GPU-unpriced", JobId: "j"},
			{DeviceUuid: "GPU-omitted", JobId: "j"}, // mapped but not in /cost
		},
	}
	cost := &CostResponse{Devices: []DeviceCost{
		{UUID: "GPU-priced", Priced: true},
		{UUID: "GPU-unpriced", Priced: false},
	}}
	out := RenderView(pack, cost)
	if !strings.Contains(out, "GPU-omitted") || !strings.Contains(out, "mfu") {
		t.Errorf("expected mfu degrade mark for omitted device:\n%s", out)
	}
	if !strings.Contains(out, "GPU-unpriced") || !strings.Contains(out, "cost") {
		t.Errorf("expected cost degrade mark for unpriced device:\n%s", out)
	}
	// A device with no missing fact gets no fabricated mark.
	marks := degradeMarks(pack, cost)
	for _, m := range marks {
		if m.DeviceUUID == "GPU-priced" {
			t.Errorf("fabricated a degrade mark for a fully-present device: %+v", m)
		}
	}
}

// TestSignalsParsedViaGenType is an explicit assertion that /signals is decoded
// into the real gpufleet.v1.EvidencePack proto message (protojson), not a JSON
// struct mirror.
func TestSignalsParsedViaGenType(t *testing.T) {
	srv := agentStub(t)
	defer srv.Close()
	pack, err := NewClient(srv.URL).Signals(context.Background())
	if err != nil {
		t.Fatalf("Signals: %v", err)
	}
	// Compile-time + runtime proof: it is *gpufleetv1.EvidencePack.
	var _ *gpufleetv1.EvidencePack = pack
	if pack.ProtoReflect().Descriptor().FullName() != "gpufleet.v1.EvidencePack" {
		t.Fatalf("not the gen EvidencePack: %s", pack.ProtoReflect().Descriptor().FullName())
	}
}

// TestClientIssuesOnlyGET locks in the read-only-by-construction invariant: every
// request the cli client makes (Signals + Cost) MUST be a GET. A stub records the
// method (and Accept header) of each request and fails on any non-GET verb, so a
// future change that introduces a write/upload would break this test.
func TestClientIssuesOnlyGET(t *testing.T) {
	signals := realSignalsProtoJSON(t)
	costJSON := `{"devices":[],"jobs":[]}`

	var methods []string
	mux := http.NewServeMux()
	record := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			methods = append(methods, r.Method)
			if r.Method != http.MethodGet {
				t.Errorf("cli issued a non-GET request: %s %s", r.Method, r.URL.Path)
				http.Error(w, "read-only", http.StatusMethodNotAllowed)
				return
			}
			if acc := r.Header.Get("Accept"); acc != "application/json" {
				t.Errorf("expected Accept: application/json on %s, got %q", r.URL.Path, acc)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}
	}
	mux.HandleFunc("/signals", record(signals))
	mux.HandleFunc("/cost", record(costJSON))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL)
	ctx := context.Background()
	if _, err := c.Signals(ctx); err != nil {
		t.Fatalf("Signals: %v", err)
	}
	if _, err := c.Cost(ctx); err != nil {
		t.Fatalf("Cost: %v", err)
	}

	if len(methods) != 2 {
		t.Fatalf("expected exactly 2 requests (Signals + Cost), got %d: %v", len(methods), methods)
	}
	for _, m := range methods {
		if m != http.MethodGet {
			t.Errorf("read-only invariant violated: non-GET method %q", m)
		}
	}
}

func lineContaining(s, sub string) string {
	for _, ln := range strings.Split(s, "\n") {
		if strings.Contains(ln, sub) {
			return ln
		}
	}
	return ""
}
