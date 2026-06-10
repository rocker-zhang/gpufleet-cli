package cli

import (
	"strings"
	"testing"

	"github.com/rocker-zhang/gpufleet-agent"
)

func TestRenderJobViewDeterministic(t *testing.T) {
	ev := agent.Evidence{
		Source: "test",
		Devices: []agent.DeviceMetrics{
			{UUID: "GPU-2", Node: "n", Model: "A10", WindowSeconds: 60, AchievedFLOPs: 3e15, TensorActiveSecs: 20},
			{UUID: "GPU-1", Node: "n", Model: "A10", WindowSeconds: 60, AchievedFLOPs: 6e15, TensorActiveSecs: 42},
		},
	}
	out, err := RenderJobView("job-x", ev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "job job-x") {
		t.Errorf("missing job header:\n%s", out)
	}
	// GPU-1 must render before GPU-2 (deterministic sort by UUID).
	if strings.Index(out, "GPU-1") > strings.Index(out, "GPU-2") {
		t.Errorf("devices not rendered in deterministic UUID order:\n%s", out)
	}
	if !strings.Contains(out, "straggler:") || !strings.Contains(out, "cost (window):") {
		t.Errorf("missing efficiency/cost lines:\n%s", out)
	}
	// Clean devices (no fault signals) must show the deterministic ABSTAIN.
	if !strings.Contains(out, "rca=ABSTAIN") {
		t.Errorf("expected ABSTAIN for clean devices:\n%s", out)
	}
}

func TestRenderJobViewRCAFires(t *testing.T) {
	// Two independent signals (Xid 79 + ECC DBE delta) -> deterministic FIRE.
	ev := agent.Evidence{Source: "test", Devices: []agent.DeviceMetrics{
		{UUID: "GPU-1", Node: "n", Model: "A10", WindowSeconds: 60,
			AchievedFLOPs: 1e15, TensorActiveSecs: 10,
			ECCDoubleBitErrs: 2, XIDs: []int{79}},
	}}
	out, err := RenderJobView("job-fire", ev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "rca=FIRE:xid79_gpu_fell_off_bus") {
		t.Errorf("expected RCA to FIRE on 2 corroborating signals:\n%s", out)
	}

	// Same input renders byte-identical output.
	out2, _ := RenderJobView("job-fire", ev)
	if out != out2 {
		t.Errorf("render not deterministic")
	}
}

func TestRenderJobViewUnknownModel(t *testing.T) {
	ev := agent.Evidence{Source: "test", Devices: []agent.DeviceMetrics{
		{UUID: "GPU-1", Model: "UNKNOWN", WindowSeconds: 1, AchievedFLOPs: 1},
	}}
	if _, err := RenderJobView("j", ev); err == nil {
		t.Fatalf("expected error for unconfigured model peak")
	}
}

func TestRootViewCommand(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"view", "--node", "ci", "--job", "j1"})
	var sb strings.Builder
	root.SetOut(&sb)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(sb.String(), "job j1") {
		t.Errorf("view output missing job header:\n%s", sb.String())
	}
}
