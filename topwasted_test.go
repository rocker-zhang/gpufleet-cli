package cli

import (
	"strings"
	"testing"
)

// digestSection returns the substring of out from the "TOP WASTED-$" header up
// to the next blank-line section break, so assertions target the digest only.
func digestSection(out string) string {
	start := strings.Index(out, "TOP WASTED-$")
	if start < 0 {
		return ""
	}
	rest := out[start:]
	// The next section begins after a "\n\n" following the digest's NOTE line.
	if i := strings.Index(rest, "\n\nJOBS"); i >= 0 {
		return rest[:i]
	}
	if i := strings.Index(rest, "\n\nDEGRADED"); i >= 0 {
		return rest[:i]
	}
	if i := strings.Index(rest, "\n\nRCA"); i >= 0 {
		return rest[:i]
	}
	return rest
}

func costResp(devs ...DeviceCost) *CostResponse {
	return &CostResponse{Devices: devs}
}

// TestTopWastedDigestRankAndTotal: the digest header is present, lists offenders
// ranked by wasted-$ descending, and prints the window total over priced devices.
func TestTopWastedDigestRankAndTotal(t *testing.T) {
	cost := costResp(
		DeviceCost{UUID: "GPU-low", Node: "n1", MFU: 0.50, IdleFraction: 0.50, WastedUSD: 0.10, UsdPerHour: 0.20, Priced: true},
		DeviceCost{UUID: "GPU-hi", Node: "n1", MFU: 0.05, IdleFraction: 0.95, WastedUSD: 0.90, UsdPerHour: 1.80, Priced: true},
		DeviceCost{UUID: "GPU-mid", Node: "n1", MFU: 0.30, IdleFraction: 0.70, WastedUSD: 0.40, UsdPerHour: 0.80, Priced: true},
	)
	out := RenderView(nil, cost, nil)
	dg := digestSection(out)
	if dg == "" {
		t.Fatalf("no TOP WASTED-$ digest section:\n%s", out)
	}
	// Honest device-level header note.
	if !strings.Contains(dg, "device-level") || !strings.Contains(dg, "no job label") {
		t.Errorf("digest header must state device-level honesty:\n%s", dg)
	}
	// Ranked descending: GPU-hi (0.90) before GPU-mid (0.40) before GPU-low (0.10).
	iHi := strings.Index(dg, "GPU-hi")
	iMid := strings.Index(dg, "GPU-mid")
	iLow := strings.Index(dg, "GPU-low")
	if !(iHi >= 0 && iHi < iMid && iMid < iLow) {
		t.Errorf("offenders not ranked by wasted-$ desc (hi<mid<low):\n%s", dg)
	}
	// Window total = 0.10+0.90+0.40 = 1.40; burn = 0.20+1.80+0.80 = 2.80.
	if !strings.Contains(dg, "$1.4000") {
		t.Errorf("missing window total $1.4000:\n%s", dg)
	}
	if !strings.Contains(dg, "$2.8000/hr") {
		t.Errorf("missing idle burn rate $2.8000/hr:\n%s", dg)
	}
	// Single-window scope + follow-up note present.
	if !strings.Contains(dg, "single-window") || !strings.Contains(dg, "TASK-0055") {
		t.Errorf("digest must carry the single-window / follow-up scope note:\n%s", dg)
	}
}

// TestTopWastedDigestUnpricedNoFabrication: an unpriced device shows n/a (never
// $0), is excluded from the total, and is reported in the coverage count.
func TestTopWastedDigestUnpricedNoFabrication(t *testing.T) {
	cost := costResp(
		DeviceCost{UUID: "GPU-priced", Node: "n1", MFU: 0.10, IdleFraction: 0.90, WastedUSD: 0.50, UsdPerHour: 1.00, Priced: true},
		DeviceCost{UUID: "GPU-noprice", Node: "n1", MFU: 0.01, IdleFraction: 0.99, WastedUSD: 0, UsdPerHour: 0, Priced: false},
	)
	out := RenderView(nil, cost, nil)
	dg := digestSection(out)
	npLine := ""
	for _, ln := range strings.Split(dg, "\n") {
		if strings.Contains(ln, "GPU-noprice") {
			npLine = ln
		}
	}
	if npLine == "" {
		t.Fatalf("unpriced device missing from digest:\n%s", dg)
	}
	if !strings.Contains(npLine, "n/a") {
		t.Errorf("unpriced device must show n/a, not a $ value:\n%s", npLine)
	}
	if strings.Contains(npLine, "$0.0000") {
		t.Errorf("unpriced device must NOT show fabricated $0:\n%s", npLine)
	}
	// Priced device ranks first; total excludes the unpriced one.
	if strings.Index(dg, "GPU-priced") > strings.Index(dg, "GPU-noprice") {
		t.Errorf("priced device should outrank unpriced:\n%s", dg)
	}
	if !strings.Contains(dg, "$0.5000") {
		t.Errorf("window total should be the single priced device $0.5000:\n%s", dg)
	}
	if !strings.Contains(dg, "1 unpriced excluded") {
		t.Errorf("digest should report unpriced coverage:\n%s", dg)
	}
}

// TestTopWastedDigestAllUnpriced: when nothing is priced, the digest degrades to
// an honest "no priced devices" line — no fabricated $0 total.
func TestTopWastedDigestAllUnpriced(t *testing.T) {
	cost := costResp(
		DeviceCost{UUID: "GPU-a", Node: "n1", MFU: 0.1, IdleFraction: 0.9, Priced: false},
		DeviceCost{UUID: "GPU-b", Node: "n1", MFU: 0.2, IdleFraction: 0.8, Priced: false},
	)
	out := RenderView(nil, cost, nil)
	dg := digestSection(out)
	if !strings.Contains(dg, "no priced devices") {
		t.Errorf("all-unpriced should degrade to a no-priced-devices line:\n%s", dg)
	}
	if strings.Contains(dg, "window total wasted-$") {
		t.Errorf("must not print a window total when nothing is priced:\n%s", dg)
	}
}

// TestTopWastedDigestDeterministic: identical input renders the digest byte-
// identically (including the stable UUID tie-break on equal waste).
func TestTopWastedDigestDeterministic(t *testing.T) {
	cost := costResp(
		DeviceCost{UUID: "GPU-2", Node: "n1", WastedUSD: 0.5, UsdPerHour: 1, Priced: true},
		DeviceCost{UUID: "GPU-1", Node: "n1", WastedUSD: 0.5, UsdPerHour: 1, Priced: true},
	)
	a := RenderView(nil, cost, nil)
	b := RenderView(nil, cost, nil)
	if a != b {
		t.Fatalf("digest render not deterministic")
	}
	dg := digestSection(a)
	// Equal waste => UUID ascending tie-break: GPU-1 before GPU-2.
	if strings.Index(dg, "GPU-1") > strings.Index(dg, "GPU-2") {
		t.Errorf("equal-waste tie-break must be UUID ascending:\n%s", dg)
	}
}
