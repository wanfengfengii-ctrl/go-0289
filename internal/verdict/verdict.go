// Package verdict computes contamination closures, creates retest
// generations and arbitrates the final batch decision.
package verdict

import (
	"sort"

	"edna-contamination-verdict/internal/protocol"
)

// ComponentName is the stable identity of this component.
const ComponentName = "contamination-closure-and-retest-generation"

// ContaminationSet is the sorted minimal set of affected wells.
type ContaminationSet struct {
	Seeds        []protocol.WellRef `json:"seeds"`
	Closure      []protocol.WellRef `json:"closure"`
	SourceDigest string             `json:"source_digest"`
}

// Generation is a new retest generation that reopens only affected wells.
type Generation struct {
	Number        int                `json:"number"`
	ReopenedWells []protocol.WellRef `json:"reopened_wells"`
	ParentDigest  string             `json:"parent_digest"`
}

// FinalType is the terminal batch decision.
type FinalType string

const (
	FinalRelease    FinalType = "release"
	FinalQuarantine FinalType = "quarantine"
	FinalVoid       FinalType = "void"
)

// Review is one qualified reviewer's attestation over an evidence digest.
type Review struct {
	ReviewerID    string `json:"reviewer_id"`
	Qualification string `json:"qualification"`
	Digest        string `json:"digest"`
}

// FinalDecision is the single persisted terminal outcome.
type FinalDecision struct {
	Type   FinalType `json:"type"`
	Seq    int64     `json:"seq"`
	Digest string    `json:"digest"`
}

// OrderedClosure returns the contamination closure sorted by plate, row and
// column without mutating the receiver.
func (c ContaminationSet) OrderedClosure() []protocol.WellRef {
	out := make([]protocol.WellRef, len(c.Closure))
	copy(out, c.Closure)
	SortWells(out)
	return out
}

// SortWells orders wells by plate, then row, then column.
func SortWells(wells []protocol.WellRef) {
	sort.Slice(wells, func(i, j int) bool {
		if wells[i].Plate != wells[j].Plate {
			return wells[i].Plate < wells[j].Plate
		}
		if wells[i].Row != wells[j].Row {
			return wells[i].Row < wells[j].Row
		}
		return wells[i].Col < wells[j].Col
	})
}
