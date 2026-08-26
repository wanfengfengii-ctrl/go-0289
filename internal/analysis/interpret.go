package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"edna-contamination-verdict/internal/protocol"
)

// Crossing is the raw threshold-crossing detection result.
type Crossing struct {
	CrossFrom int  `json:"cross_from"`
	CrossTo   int  `json:"cross_to"`
	WindowMet bool `json:"window_met"`
	Crossed   bool `json:"crossed"`
}

// DetectCrossing scans a baseline-subtracted curve for the first up-crossing
// of threshold, where "hit" is defined as value >= threshold. It requires
// window consecutive hits after the crossing for WindowMet to be true. The
// boundary value (value == threshold) counts as a hit.
func DetectCrossing(curve []int64, threshold int64, window int) Crossing {
	if len(curve) < 2 || window < 1 {
		return Crossing{}
	}
	for i := 0; i < len(curve)-1; i++ {
		if curve[i] < threshold && curve[i+1] >= threshold {
			c := Crossing{CrossFrom: i, CrossTo: i + 1, Crossed: true}
			hits := 0
			for j := i + 1; j < len(curve) && hits < window; j++ {
				if curve[j] >= threshold {
					hits++
				} else {
					break
				}
			}
			c.WindowMet = hits >= window
			return c
		}
	}
	return Crossing{}
}

// Interpret runs the full fixed-point pipeline for a single well's curve:
// baseline subtraction, threshold crossing detection and cycle-threshold
// interpolation. cycleStart is the cycle number of curve[0].
func Interpret(curve []int64, cycleStart int, snap protocol.ProtocolSnapshot) (Interpretation, error) {
	out := Interpretation{
		Baseline: Baseline{Start: snap.BaselineStart, End: snap.BaselineEnd},
	}
	base, err := BaselineMean(curve, snap.BaselineStart, snap.BaselineEnd)
	if err != nil {
		return out, err
	}
	out.Baseline.Value = base
	sub, err := SubtractBaseline(curve, base)
	if err != nil {
		return out, err
	}
	c := DetectCrossing(sub, snap.Threshold, snap.Window)
	if !c.Crossed {
		out.Positive = false
		out.Digest = InterpretationDigest(out)
		return out, nil
	}
	out.FirstCrossFrom = cycleStart + c.CrossFrom
	out.FirstCrossTo = cycleStart + c.CrossTo
	out.WindowMet = c.WindowMet
	out.Positive = c.WindowMet

	x0 := int64(cycleStart + c.CrossFrom)
	x1 := int64(cycleStart + c.CrossTo)
	y0 := sub[c.CrossFrom]
	y1 := sub[c.CrossTo]
	ct, err := InterpolateCt(x0, y0, x1, y1, snap.Threshold, snap.Scale)
	if err != nil {
		return out, err
	}
	out.ScaledCt = ct
	out.Digest = InterpretationDigest(out)
	return out, nil
}

// InterpretationDigest derives a stable content digest for an interpretation.
func InterpretationDigest(i Interpretation) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%d|%d|%t|%t|%s|%s",
		i.Well.Key(), i.Baseline.Value, i.FirstCrossFrom, i.ScaledCt,
		i.Positive, i.WindowMet, i.Control, i.Replicate)
	return hex.EncodeToString(h.Sum(nil))
}

// ControlVerdictFor gates a control well using its detection result. A
// positive control must be positive and fall within the bounded scaled-cycle
// range; a negative-acting control (negative, extraction blank, no-template)
// must remain negative (never cross the threshold).
func ControlVerdictFor(interp Interpretation, snap protocol.ProtocolSnapshot, positiveControl bool) ControlVerdict {
	if positiveControl {
		if interp.WindowMet && interp.ScaledCt >= snap.PositiveMin && interp.ScaledCt <= snap.PositiveMax {
			return ControlPass
		}
		return ControlFail
	}
	if interp.WindowMet {
		return ControlFail
	}
	return ControlPass
}

// ReplicateVerdictFor reports whether a set of replicate positive calls are
// mutually consistent (all share the same positive/negative call).
func ReplicateVerdictFor(calls []bool) ReplicateVerdict {
	if len(calls) < 2 {
		return ReplicateConsistent
	}
	first := calls[0]
	for _, c := range calls[1:] {
		if c != first {
			return ReplicateMismatch
		}
	}
	return ReplicateConsistent
}
