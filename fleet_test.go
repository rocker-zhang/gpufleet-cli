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

func sampleEnvelope() *gpufleetv1.AggregationEnvelope {
	return &gpufleetv1.AggregationEnvelope{
		ContractVersion: "v0.2.0",
		TotalNodes:      2,
		FaultedNodes:    1,
		AbstainedNodes:  1,
		Entries: []*gpufleetv1.FleetEntry{
			{NodeId: "node-a", JobId: "job-1", DeviceUuid: "GPU-1",
				Verdict: &gpufleetv1.Verdict{
					FaultClass: gpufleetv1.FaultClass_FAULT_CLASS_GPU_FALLEN_OFF_BUS,
					Confidence: 0.97,
				}},
			{NodeId: "node-b", JobId: "", DeviceUuid: "",
				Verdict: &gpufleetv1.Verdict{FaultClass: gpufleetv1.FaultClass_FAULT_CLASS_ABSTAIN}},
		},
	}
}

// fleetServer is a stub control plane that serves a fixed envelope on /v1/fleet
// and optionally requires the license header.
func fleetServer(t *testing.T, requireToken bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/fleet" || r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if requireToken && r.Header.Get("Authorization") != "Bearer good-token" {
			w.WriteHeader(http.StatusPaymentRequired)
			return
		}
		out, _ := protojson.Marshal(sampleEnvelope())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	}))
}

func TestFleetClient_FetchAndDecode(t *testing.T) {
	srv := fleetServer(t, false)
	defer srv.Close()

	env, err := NewFleetClient(srv.URL, "").Fleet(context.Background())
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if env.GetTotalNodes() != 2 || env.GetFaultedNodes() != 1 {
		t.Errorf("counts total=%d faulted=%d", env.GetTotalNodes(), env.GetFaultedNodes())
	}
	if len(env.GetEntries()) != 2 {
		t.Fatalf("entries=%d, want 2", len(env.GetEntries()))
	}
}

func TestFleetClient_NoEndpointIsUsageError(t *testing.T) {
	_, err := NewFleetClient("", "").Fleet(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no control-plane URL") {
		t.Fatalf("want usage error, got %v", err)
	}
}

func TestFleetClient_LicenseRequired(t *testing.T) {
	srv := fleetServer(t, true)
	defer srv.Close()

	_, err := NewFleetClient(srv.URL, "wrong").Fleet(context.Background())
	if err == nil || !strings.Contains(err.Error(), "license") {
		t.Fatalf("want license error, got %v", err)
	}
	// correct token succeeds
	if _, err := NewFleetClient(srv.URL, "good-token").Fleet(context.Background()); err != nil {
		t.Fatalf("valid token should succeed: %v", err)
	}
}

func TestRenderFleet_Deterministic(t *testing.T) {
	env := sampleEnvelope()
	out := RenderFleet(env, "https://cp.example", false)

	a := strings.Index(out, "node-a")
	b := strings.Index(out, "node-b")
	if a < 0 || b < 0 || a > b {
		t.Errorf("nodes not present/sorted: a=%d b=%d", a, b)
	}
	if !strings.Contains(out, "GPU_FALLEN_OFF_BUS") {
		t.Error("fired class not rendered verbatim")
	}
	if !strings.Contains(out, "ABSTAIN") {
		t.Error("abstain not rendered")
	}
	if !strings.Contains(out, "2 node(s)  1 faulted  1 abstained") {
		t.Errorf("summary line missing/wrong:\n%s", out)
	}
	// Repeated render is byte-identical (no wall-clock).
	if out != RenderFleet(env, "https://cp.example", false) {
		t.Error("render not deterministic")
	}
}

func TestRenderFleet_Empty(t *testing.T) {
	out := RenderFleet(&gpufleetv1.AggregationEnvelope{ContractVersion: "v0.2.0"}, "https://cp.example", false)
	if !strings.Contains(out, "no nodes") {
		t.Errorf("empty envelope should say no nodes:\n%s", out)
	}
}
