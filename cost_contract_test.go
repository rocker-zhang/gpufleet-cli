package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	gpufleetv1 "github.com/rocker-zhang/gpufleet-proto/gen/go/gpufleet/v1"
)

// TestCostWireContractGolden is the consumer-side half of the /cost anti-drift
// pair (agent CLAUDE.md §5a). It decodes the committed golden fixture —
// byte-identical to agent/testdata/cost_golden.json, committed here because cli
// must NOT import the agent Go module — into cli's HAND-COPIED CostResponse DTO
// and asserts the field shape, most importantly that usd_per_hour decodes and is
// the per-HOUR rate distinct from per-window wasted_usd. If the agent's wire
// shape and cli's DTO drift apart, DisallowUnknownFields makes this fail.
func TestCostWireContractGolden(t *testing.T) {
	b, err := os.ReadFile("testdata/cost_golden.json")
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}

	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var cost CostResponse
	if err := dec.Decode(&cost); err != nil {
		t.Fatalf("golden /cost does not match cli CostResponse DTO (drift?): %v", err)
	}

	byUUID := map[string]DeviceCost{}
	for _, d := range cost.Devices {
		byUUID[d.UUID] = d
	}

	// Priced idle device: usd_per_hour decodes, is > 0, and (sub-hour window)
	// strictly exceeds the per-window wasted_usd — proving the two are distinct.
	idle, ok := byUUID["GPU-mock-0002"]
	if !ok {
		t.Fatalf("golden missing GPU-mock-0002")
	}
	if !idle.Priced {
		t.Fatalf("GPU-mock-0002 must be priced")
	}
	if idle.UsdPerHour <= 0 {
		t.Errorf("usd_per_hour did not decode into cli DTO (got %v)", idle.UsdPerHour)
	}
	if idle.UsdPerHour <= idle.WastedUSD {
		t.Errorf("idle per-hour rate (%v) must exceed per-window waste (%v)", idle.UsdPerHour, idle.WastedUSD)
	}

	// Unpriced device: priced==false ⇒ $ fields zero, no meaning.
	un, ok := byUUID["GPU-mock-unpriced"]
	if !ok {
		t.Fatalf("golden missing GPU-mock-unpriced")
	}
	if un.Priced || un.UsdPerHour != 0 || un.WastedUSD != 0 {
		t.Errorf("unpriced device shape wrong: %+v", un)
	}

	// Jobs carry the aggregate usd_per_hour.
	if len(cost.Jobs) == 0 {
		t.Fatalf("golden missing jobs")
	}
	for _, j := range cost.Jobs {
		if j.Priced && j.UsdPerHour <= 0 {
			t.Errorf("priced job %s missing usd_per_hour, got %v", j.JobID, j.UsdPerHour)
		}
	}
}

// TestRenderPerHourColumn proves RenderView emits a real $/hr column from the
// golden fixture: a priced idle device shows a concrete $/hr value greater than
// its windowed waste, while an UNPRICED device shows the degrade mark `n/a`
// (never a fabricated $0) in the $/hr position.
func TestRenderPerHourColumn(t *testing.T) {
	b, err := os.ReadFile("testdata/cost_golden.json")
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	var cost CostResponse
	if err := json.Unmarshal(b, &cost); err != nil {
		t.Fatalf("decode golden: %v", err)
	}

	// Minimal pack so the unpriced device also carries an mfu degrade mark path;
	// not required for the $/hr assertions but keeps the render realistic.
	pack := &gpufleetv1.EvidencePack{AgentId: "golden"}

	// Render with FULL UUIDs: this test matches device rows by their complete
	// UUID (e.g. "GPU-mock-unpriced", 17 chars) so it must use the --full-uuid
	// render; the default short-prefix path is covered by view_test.go
	// (TASK-0043). The $ values asserted below are display-identical in both.
	out := RenderViewFull(pack, &cost)

	// Header carries the new $/hr column alongside waste(win).
	if !strings.Contains(out, "$/hr") {
		t.Fatalf("render missing $/hr column header:\n%s", out)
	}
	if !strings.Contains(out, "waste(win)") {
		t.Errorf("render dropped the waste(win) column:\n%s", out)
	}

	idleLine := lineContaining(out, "GPU-mock-0002")
	if idleLine == "" {
		t.Fatalf("no GPU-mock-0002 line:\n%s", out)
	}
	// Idle device per-hour rate ($1.0450) is rendered, and exceeds its windowed
	// waste ($0.0174) — both must appear on the line.
	if !strings.Contains(idleLine, "$1.0450") {
		t.Errorf("idle device $/hr (1.0450) not rendered:\n%s", idleLine)
	}
	if !strings.Contains(idleLine, "$0.0174") {
		t.Errorf("idle device waste(win) (0.0174) not rendered:\n%s", idleLine)
	}

	// Unpriced device: BOTH money columns show the degrade mark n/a, not $0.
	unLine := lineContaining(out, "GPU-mock-unpriced")
	if unLine == "" {
		t.Fatalf("no GPU-mock-unpriced line:\n%s", out)
	}
	if strings.Contains(unLine, "$0.0000") {
		t.Errorf("unpriced device must NOT fabricate $0 in a money column:\n%s", unLine)
	}
	if strings.Count(unLine, "n/a") < 2 {
		t.Errorf("unpriced device must degrade-mark both waste(win) and $/hr with n/a:\n%s", unLine)
	}

	// Jobs table also carries a $/hr value for a priced job.
	jobLine := lineContaining(out, "job-idle-b")
	if jobLine == "" {
		t.Fatalf("no job-idle-b line:\n%s", out)
	}
	if !strings.Contains(jobLine, "$1.0450") {
		t.Errorf("priced job $/hr not rendered:\n%s", jobLine)
	}
}
