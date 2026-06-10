// Package cli wires the open gpufleet libraries (semantics + agent + rca)
// together locally to render a job-level utilization/cost view. It is
// standalone-useful with NO control-plane and contains NO closed logic.
package cli

import (
	"fmt"
	"strings"

	"github.com/rocker-zhang/gpufleet-agent"
	"github.com/rocker-zhang/gpufleet-rca"
	"github.com/rocker-zhang/gpufleet-semantics"
)

// windowFromMetrics builds an rca.Window from a device's normalized evidence.
// It maps the agent's raw observations onto independent rca signal names so the
// deterministic engine can apply the >=2-signal gate. The CLI does no
// adjudication itself — it only renders whatever the engine deterministically
// decides (FIRE with cited signals, or ABSTAIN).
func windowFromMetrics(d agent.DeviceMetrics) rca.Window {
	var sigs []rca.Signal
	for _, xid := range d.XIDs {
		if xid == 79 {
			sigs = append(sigs, rca.Signal{Name: "dmesg.xid79", Detail: "Xid 79"})
		}
	}
	if d.ECCDoubleBitErrs > 0 {
		sigs = append(sigs, rca.Signal{
			Name:   "ecc.dbe.delta",
			Detail: fmt.Sprintf("dbe delta=%d", d.ECCDoubleBitErrs),
		})
	}
	return rca.Window{DeviceUUID: d.UUID, Signals: sigs}
}

// PeakFLOPSByModel maps a device model to its peak FLOP/s for MFU math. These
// are public reference numbers; the real values are supplied via config.
var PeakFLOPSByModel = map[string]float64{
	"A10":  1.25e14, // ~125 TFLOP/s BF16 tensor (reference)
	"GB10": 1.0e15,  // placeholder reference peak
}

// CostPerHourByModel maps a device model to a reference $/hour for attribution.
var CostPerHourByModel = map[string]float64{
	"A10":  1.20,
	"GB10": 4.00,
}

// RenderJobView turns a collected evidence pack into a deterministic,
// human-readable job-level utilization/cost view. All devices in the evidence
// are attributed to the single given job (the local CLI does not resolve
// multi-job ownership; that is the control plane's concern).
func RenderJobView(jobID string, ev agent.Evidence) (string, error) {
	var devs []semantics.DeviceEfficiency
	for _, d := range ev.Devices {
		spec := semantics.DeviceSpec{
			PeakFLOPS:   PeakFLOPSByModel[d.Model],
			CostPerHour: CostPerHourByModel[d.Model],
		}
		if spec.PeakFLOPS <= 0 {
			return "", fmt.Errorf("cli: no peak FLOPS configured for model %q", d.Model)
		}
		eff, err := semantics.DeviceEff(semantics.DeviceSample{
			Device:           semantics.Device{UUID: d.UUID, Node: d.Node, Model: d.Model},
			WindowSeconds:    d.WindowSeconds,
			AchievedFLOPs:    d.AchievedFLOPs,
			TensorActiveSecs: d.TensorActiveSecs,
		}, spec)
		if err != nil {
			return "", fmt.Errorf("cli: device %s: %w", d.UUID, err)
		}
		devs = append(devs, eff)
	}

	job := semantics.JobEff(semantics.Job{ID: jobID}, devs)

	// Run the deterministic RCA engine over each device's evidence window.
	eng := rca.NewEngine(rca.XID79{})
	byUUID := make(map[string]agent.DeviceMetrics, len(ev.Devices))
	for _, d := range ev.Devices {
		byUUID[d.UUID] = d
	}

	var b strings.Builder
	fmt.Fprintf(&b, "job %s  (source=%s)\n", job.Job.ID, ev.Source)
	fmt.Fprintf(&b, "  mean MFU:       %.3f\n", job.MeanMFU)
	fmt.Fprintf(&b, "  straggler:      %.3f\n", job.StragglerRatio)
	fmt.Fprintf(&b, "  cost (window):  $%.4f\n", job.CostUSD)
	fmt.Fprintf(&b, "  devices:\n")
	for _, d := range job.Devices {
		v := eng.Evaluate(windowFromMetrics(byUUID[d.Device.UUID]))
		rcaCol := v.Outcome.String()
		if v.Outcome == rca.Fire {
			rcaCol = fmt.Sprintf("FIRE:%s[%s]", v.FaultClass, strings.Join(v.CitedSignals, ","))
		}
		fmt.Fprintf(&b, "    %-16s mfu=%.3f tensor=%.3f $%.4f  rca=%s\n",
			d.Device.UUID, d.MFU, d.TensorActive, d.CostUSD, rcaCol)
	}
	return b.String(), nil
}
