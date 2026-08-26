// Package protocol defines the immutable experiment rules and the locked
// protocol snapshot that governs plate layout, controls, replicate
// relationships and contamination propagation edges.
package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
)

// ComponentName is the stable identity of this component.
const ComponentName = "experiment-rules-and-plate-lock"

// ControlKind identifies the role a well plays in the experiment.
type ControlKind string

const (
	// PositiveControl must cross the threshold within a bounded cycle range.
	PositiveControl ControlKind = "positive"
	// NegativeControl must never cross the threshold.
	NegativeControl ControlKind = "negative"
	// ExtractionBlank is a reagent-only well used to detect extraction carry-over.
	ExtractionBlank ControlKind = "extraction_blank"
	// NoTemplateControl is a template-free control.
	NoTemplateControl ControlKind = "no_template"
)

// WellRef uniquely identifies a well by plate, row and column.
type WellRef struct {
	Plate string `json:"plate"`
	Row   int    `json:"row"`
	Col   int    `json:"col"`
}

// Key returns the stable canonical coordinate key of the well.
func (w WellRef) Key() string {
	return fmt.Sprintf("%s:%d:%d", w.Plate, w.Row, w.Col)
}

// PropagationEdge is a directed contamination edge between two wells.
type PropagationEdge struct {
	From WellRef `json:"from"`
	To   WellRef `json:"to"`
}

// ProtocolSnapshot is the immutable rule summary produced when a batch is
// locked. It can never be mutated after creation.
type ProtocolSnapshot struct {
	Target         string            `json:"target"`
	Scale          int64             `json:"scale"`
	Threshold      int64             `json:"threshold"`
	BaselineStart  int               `json:"baseline_start"`
	BaselineEnd    int               `json:"baseline_end"`
	Window         int               `json:"window"`
	PositiveMin    int64             `json:"positive_min"`
	PositiveMax    int64             `json:"positive_max"`
	ReplicateCount int               `json:"replicate_count"`
	ReagentLot     string            `json:"reagent_lot"`
	Edges          []PropagationEdge `json:"edges"`
	Digest         string            `json:"digest"`
}

// ComputeDigest returns a stable, content-addressed summary of the snapshot.
// Edge order does not affect the result.
func (s ProtocolSnapshot) ComputeDigest() string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%d|%d|%d|%d|%d|%d|%d|%s|",
		s.Target, s.Scale, s.Threshold, s.BaselineStart, s.BaselineEnd, s.Window,
		s.PositiveMin, s.PositiveMax, s.ReplicateCount, s.ReagentLot)
	type keyed struct{ from, to string }
	ks := make([]keyed, 0, len(s.Edges))
	for _, e := range s.Edges {
		ks = append(ks, keyed{e.From.Key(), e.To.Key()})
	}
	sort.Slice(ks, func(i, j int) bool {
		if ks[i].from != ks[j].from {
			return ks[i].from < ks[j].from
		}
		return ks[i].to < ks[j].to
	})
	for _, k := range ks {
		fmt.Fprintf(h, "%s->%s;", k.from, k.to)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ErrStaleDigest indicates a request carried an outdated rule summary.
var ErrStaleDigest = errors.New("stale protocol digest")
