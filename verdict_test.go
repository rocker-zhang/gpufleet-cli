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

// fireVerdictProtoJSON builds a REAL gpufleet.v1.Verdict (the generated gen type,
// not a mirror) for a FIRED window — the injected XID79/GPU-fallen-off-bus demo
// pattern from agent CLAUDE.md §5f, with TWO cited signals carrying DISTINCT
// sources (DCGM + DMESG_XID) so the >=2-independent-signal gate genuinely fired —
// and marshals it with protojson exactly as the agent's /verdict serves it. This
// proves cli parses the canonical wire shape with the real gen type.
func fireVerdictProtoJSON(t *testing.T) string {
	t.Helper()
	v := &gpufleetv1.Verdict{
		ContractVersion: "v1",
		FaultClass:      gpufleetv1.FaultClass_FAULT_CLASS_GPU_FALLEN_OFF_BUS,
		Confidence:      0.95,
		CitedSignals: []*gpufleetv1.CitedSignal{
			{SignalId: "device.lost.dcgm.GPU-mock-0001", Source: gpufleetv1.SignalSource_SIGNAL_SOURCE_DCGM, Note: "device fell off the bus"},
			{SignalId: "dmesg.xid79.GPU-mock-0001", Source: gpufleetv1.SignalSource_SIGNAL_SOURCE_DMESG_XID, Note: "XID 79 GPU has fallen off the bus"},
		},
		PlaybookId: "GATE_SIGNATURE_XID79_FALLEN_OFF_BUS",
		Signature:  gpufleetv1.GateSignature_GATE_SIGNATURE_XID79_FALLEN_OFF_BUS,
	}
	b, err := protojson.Marshal(v)
	if err != nil {
		t.Fatalf("marshal FIRE Verdict: %v", err)
	}
	return string(b)
}

// abstainVerdictProtoJSON builds a REAL gpufleet.v1.Verdict for the ABSTAIN window
// (the agent's default no-inject behavior, agent CLAUDE.md §5f): the safe default —
// FAULT_CLASS_ABSTAIN, confidence 1.0 (confidence IN abstaining), empty cited
// signals, GATE_SIGNATURE_UNSPECIFIED — served as canonical protojson.
func abstainVerdictProtoJSON(t *testing.T) string {
	t.Helper()
	v := &gpufleetv1.Verdict{
		ContractVersion: "v1",
		FaultClass:      gpufleetv1.FaultClass_FAULT_CLASS_ABSTAIN,
		Confidence:      1.0,
		Signature:       gpufleetv1.GateSignature_GATE_SIGNATURE_UNSPECIFIED,
	}
	b, err := protojson.Marshal(v)
	if err != nil {
		t.Fatalf("marshal ABSTAIN Verdict: %v", err)
	}
	return string(b)
}

// verdictStub stands up an httptest agent serving the cost stub plus a /verdict
// returning the given protojson body. /signals is served too so the full view
// renders. Non-GET on /verdict is 405 (read-only, RULES §A).
func verdictStub(t *testing.T, verdictJSON string) *httptest.Server {
	t.Helper()
	signals := realSignalsProtoJSON(t)
	costJSON := `{
	  "devices": [
	    {"uuid":"GPU-idle","node":"n1","mfu":0.01,"tensor_active":0.02,"idle_fraction":0.95,"cost_usd":0.02,"wasted_usd":0.019,"usd_per_hour":1.14,"priced":true,"low_utilization":true}
	  ],
	  "jobs": []
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
	mux.HandleFunc("/verdict", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "read-only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(verdictJSON))
	})
	return httptest.NewServer(mux)
}

// TestVerdictParsedViaGenType is an explicit assertion that /verdict is decoded
// into the real gpufleet.v1.Verdict proto message (protojson), not a struct mirror.
func TestVerdictParsedViaGenType(t *testing.T) {
	srv := verdictStub(t, fireVerdictProtoJSON(t))
	defer srv.Close()
	v, err := NewClient(srv.URL).Verdict(context.Background())
	if err != nil {
		t.Fatalf("Verdict: %v", err)
	}
	var _ *gpufleetv1.Verdict = v
	if v.ProtoReflect().Descriptor().FullName() != "gpufleet.v1.Verdict" {
		t.Fatalf("not the gen Verdict: %s", v.ProtoReflect().Descriptor().FullName())
	}
}

// TestRenderFireBanner is the FIRE half of the TASK-0049 DoD: a fired verdict
// renders the agent's fault class, confidence, signature, and BOTH cited signals
// (signalId @ source) VERBATIM, below the cost table. cli does not recompute.
func TestRenderFireBanner(t *testing.T) {
	srv := verdictStub(t, fireVerdictProtoJSON(t))
	defer srv.Close()

	c := NewClient(srv.URL)
	ctx := context.Background()
	pack, _ := c.Signals(ctx)
	cost, _ := c.Cost(ctx)
	verdict, err := c.Verdict(ctx)
	if err != nil {
		t.Fatalf("Verdict: %v", err)
	}

	out := RenderView(pack, cost, verdict)

	// Fault class (verbatim, prefix-stripped for display) + confidence.
	if !strings.Contains(out, "RCA: GPU_FALLEN_OFF_BUS") {
		t.Errorf("FIRE banner must show the agent's fault class:\n%s", out)
	}
	if !strings.Contains(out, "confidence 0.95") {
		t.Errorf("FIRE banner must show the agent's confidence:\n%s", out)
	}
	// Signature surfaced.
	if !strings.Contains(out, "signature GATE_SIGNATURE_XID79_FALLEN_OFF_BUS") {
		t.Errorf("FIRE banner must show the gate signature:\n%s", out)
	}
	// BOTH cited signals with their (distinct) sources rendered.
	if !strings.Contains(out, "device.lost.dcgm.GPU-mock-0001 @ DCGM") {
		t.Errorf("FIRE banner missing the DCGM cited signal:\n%s", out)
	}
	if !strings.Contains(out, "dmesg.xid79.GPU-mock-0001 @ DMESG_XID") {
		t.Errorf("FIRE banner missing the DMESG_XID cited signal:\n%s", out)
	}
	// The note that deep RCA/narration is closed; open verdict has NO narration.
	if !strings.Contains(out, "closed control plane") {
		t.Errorf("FIRE banner must note deep RCA/narration is the closed control plane:\n%s", out)
	}
	// The dead per-device column is gone.
	if strings.Contains(out, "n/a (no control plane)") {
		t.Errorf("the per-device n/a verdict column must be gone:\n%s", out)
	}
}

// TestRenderAbstainBanner is the ABSTAIN half: the default window renders the
// honest open-gate message with NO fabricated fault class.
func TestRenderAbstainBanner(t *testing.T) {
	srv := verdictStub(t, abstainVerdictProtoJSON(t))
	defer srv.Close()

	c := NewClient(srv.URL)
	ctx := context.Background()
	pack, _ := c.Signals(ctx)
	cost, _ := c.Cost(ctx)
	verdict, err := c.Verdict(ctx)
	if err != nil {
		t.Fatalf("Verdict: %v", err)
	}

	out := RenderView(pack, cost, verdict)

	if !strings.Contains(out, "RCA: ABSTAIN — open ≥2-independent-signal gate did not corroborate a fault class this window") {
		t.Errorf("ABSTAIN banner must show the honest open-gate message:\n%s", out)
	}
	// No fabricated fault class names leak into an ABSTAIN banner.
	for _, leak := range []string{"GPU_FALLEN_OFF_BUS", "ECC_UNCORRECTABLE"} {
		if strings.Contains(out, leak) {
			t.Errorf("ABSTAIN banner must not imply a fault (%q):\n%s", leak, out)
		}
	}
	// The FIRE-style "RCA: <class>  confidence <X.XX>" line must NOT appear; only
	// the closing note may contain the word "confidence".
	if strings.Contains(out, "confidence 1.00") || strings.Contains(out, "RCA: ABSTAIN  confidence") {
		t.Errorf("ABSTAIN banner must not render a fired-style confidence line:\n%s", out)
	}
}

// TestRenderNoVerdictYetGraceful proves the empty state (TASK-0042): a nil verdict
// (agent pre-window 503 or unreachable) renders an honest "(no verdict yet)" line,
// never a crash and never a fabricated class.
func TestRenderNoVerdictYetGraceful(t *testing.T) {
	pack := &gpufleetv1.EvidencePack{AgentId: "noverdict"}
	cost := &CostResponse{Devices: []DeviceCost{{UUID: "GPU-x", Priced: true}}}

	out := RenderView(pack, cost, nil)

	if !strings.Contains(out, "RCA: (no verdict yet)") {
		t.Errorf("nil verdict must render an honest no-verdict-yet line:\n%s", out)
	}
	for _, leak := range []string{"GPU_FALLEN_OFF_BUS", "ECC_UNCORRECTABLE", "ABSTAIN"} {
		if strings.Contains(out, leak) {
			t.Errorf("no-verdict-yet must not fabricate a class/state (%q):\n%s", leak, out)
		}
	}
}

// TestVerdict503IsNoVerdictYet proves a pre-window 503 on /verdict (like /signals)
// degrades to nil ("no verdict yet"), not an error — and renders gracefully.
func TestVerdict503IsNoVerdictYet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/verdict", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no signal window collected yet", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	v, err := NewClient(srv.URL).Verdict(context.Background())
	if err != nil {
		t.Fatalf("a 503 /verdict must degrade to nil, not error: %v", err)
	}
	if v != nil {
		t.Fatalf("503 /verdict must yield a nil verdict, got %+v", v)
	}
	out := RenderView(nil, &CostResponse{Devices: []DeviceCost{{UUID: "GPU-x", Priced: true}}}, v)
	if !strings.Contains(out, "RCA: (no verdict yet)") {
		t.Errorf("503 verdict must render the no-verdict-yet line:\n%s", out)
	}
}

// TestVerdictUnreachableIsNoVerdictYet proves an unreachable agent folds into the
// no-verdict-yet state for the (additive) banner rather than failing the view —
// the cost path is the authority on a down agent.
func TestVerdictUnreachableIsNoVerdictYet(t *testing.T) {
	c := NewClient("http://127.0.0.1:0") // nothing listening
	v, err := c.Verdict(context.Background())
	if err != nil {
		t.Fatalf("an unreachable /verdict must degrade to nil, not error: %v", err)
	}
	if v != nil {
		t.Fatalf("unreachable /verdict must yield nil, got %+v", v)
	}
}

// TestVerdictRenderedVerbatim is the boundary assertion: cli renders WHATEVER class
// the agent sends, with no threshold/recompute logic. An ECC verdict with the
// agent's own confidence is rendered exactly — cli does not second-guess it.
func TestVerdictRenderedVerbatim(t *testing.T) {
	v := &gpufleetv1.Verdict{
		ContractVersion: "v1",
		FaultClass:      gpufleetv1.FaultClass_FAULT_CLASS_ECC_UNCORRECTABLE,
		Confidence:      0.97,
		CitedSignals: []*gpufleetv1.CitedSignal{
			{SignalId: "ecc.dbe.GPU-mock-0002", Source: gpufleetv1.SignalSource_SIGNAL_SOURCE_DCGM},
			{SignalId: "dmesg.xid.ecc.48.GPU-mock-0002", Source: gpufleetv1.SignalSource_SIGNAL_SOURCE_DMESG_XID},
		},
		Signature: gpufleetv1.GateSignature_GATE_SIGNATURE_ECC_UNCORRECTABLE,
	}
	out := RenderView(&gpufleetv1.EvidencePack{AgentId: "verbatim"}, &CostResponse{}, v)

	if !strings.Contains(out, "RCA: ECC_UNCORRECTABLE  confidence 0.97") {
		t.Errorf("cli must render the agent's class+confidence verbatim:\n%s", out)
	}
	if !strings.Contains(out, "signature GATE_SIGNATURE_ECC_UNCORRECTABLE") {
		t.Errorf("cli must render the agent's signature verbatim:\n%s", out)
	}
}

// TestVerdictBannerDeterministic locks byte-identical output for identical input,
// including the cited-signal ordering (sorted by signalId).
func TestVerdictBannerDeterministic(t *testing.T) {
	srv := verdictStub(t, fireVerdictProtoJSON(t))
	defer srv.Close()
	c := NewClient(srv.URL)
	ctx := context.Background()
	pack, _ := c.Signals(ctx)
	cost, _ := c.Cost(ctx)
	v, _ := c.Verdict(ctx)
	if a, b := RenderView(pack, cost, v), RenderView(pack, cost, v); a != b {
		t.Errorf("verdict banner render not deterministic")
	}
}
