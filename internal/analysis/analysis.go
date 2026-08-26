// Package analysis implements fixed-point baseline calculation, threshold
// crossing detection and cycle-threshold interpolation.
package analysis

import (
	"errors"

	"edna-contamination-verdict/internal/protocol"
)

// ComponentName is the stable identity of this component.
const ComponentName = "fixed-point-interpretation-and-quality-gating"

var (
	// ErrDivideByZero indicates a zero fluorescence span during interpolation.
	ErrDivideByZero = errors.New("division by zero")
	// ErrOverflow indicates an int64 fixed-point overflow.
	ErrOverflow = errors.New("fixed-point overflow")
	// ErrInvalidRange indicates a non-increasing or empty cycle interval.
	ErrInvalidRange = errors.New("invalid interpolation range")
	// ErrInvalidScale indicates a non-positive scale factor.
	ErrInvalidScale = errors.New("invalid scale factor")
)

// Baseline is the subtracted baseline window and its mean value.
type Baseline struct {
	Start int   `json:"start"`
	End   int   `json:"end"`
	Value int64 `json:"value"`
}

// CtResult is the threshold-crossing outcome.
type CtResult struct {
	ScaledCt  int64 `json:"scaled_ct"`
	Crossed   bool  `json:"crossed"`
	WindowMet bool  `json:"window_met"`
}

// ControlVerdict is the gating result for a control well.
type ControlVerdict string

const (
	ControlPass   ControlVerdict = "pass"
	ControlFail   ControlVerdict = "fail"
	ControlNotRun ControlVerdict = "not_run"
)

// ReplicateVerdict is the consistency result across sample replicates.
type ReplicateVerdict string

const (
	ReplicateConsistent ReplicateVerdict = "consistent"
	ReplicateMismatch   ReplicateVerdict = "mismatch"
)

// Interpretation is the deterministic per-well readout.
type Interpretation struct {
	Well           protocol.WellRef `json:"well"`
	Baseline       Baseline         `json:"baseline"`
	FirstCrossFrom int              `json:"first_cross_from"`
	FirstCrossTo   int              `json:"first_cross_to"`
	ScaledCt       int64            `json:"scaled_ct"`
	Positive       bool             `json:"positive"`
	WindowMet      bool             `json:"window_met"`
	Control        ControlVerdict   `json:"control"`
	Replicate      ReplicateVerdict `json:"replicate"`
	Digest         string           `json:"digest"`
}

// InterpolateCt computes the fixed-point cycle where the fluorescence curve
// crosses threshold between points (x0,y0) and (x1,y1). The result is scaled
// by scale and rounded half away from zero.
func InterpolateCt(x0, y0, x1, y1, threshold, scale int64) (int64, error) {
	if scale <= 0 {
		return 0, ErrInvalidScale
	}
	if x1 <= x0 {
		return 0, ErrInvalidRange
	}
	dy := y1 - y0
	if dy == 0 {
		return 0, ErrDivideByZero
	}
	delta := threshold - y0
	dx := x1 - x0
	if mulOverflow(delta, dx) {
		return 0, ErrOverflow
	}
	num := delta * dx
	if mulOverflow(num, scale) {
		return 0, ErrOverflow
	}
	num *= scale
	frac, err := divRoundHalfAway(num, dy)
	if err != nil {
		return 0, err
	}
	if mulOverflow(x0, scale) {
		return 0, ErrOverflow
	}
	base := x0 * scale
	if addOverflow(base, frac) {
		return 0, ErrOverflow
	}
	return base + frac, nil
}

func mulOverflow(a, b int64) bool {
	if a == 0 || b == 0 {
		return false
	}
	r := a * b
	return r/b != a
}

func addOverflow(a, b int64) bool {
	r := a + b
	return (b > 0 && r < a) || (b < 0 && r > a)
}

// divRoundHalfAway divides n by d and rounds half away from zero.
func divRoundHalfAway(n, d int64) (int64, error) {
	if d == 0 {
		return 0, ErrDivideByZero
	}
	q := n / d
	r := n % d
	if r == 0 {
		return q, nil
	}
	absR := r
	if absR < 0 {
		absR = -absR
	}
	absD := d
	if absD < 0 {
		absD = -absD
	}
	if absR*2 >= absD {
		if (n >= 0) == (d >= 0) {
			q++
		} else {
			q--
		}
	}
	return q, nil
}
