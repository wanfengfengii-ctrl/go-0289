// Package assay models the batch, plate and wells together with sample
// loading and instrument curve-chunk ingestion.
package assay

import (
	"errors"
	"sort"

	"edna-contamination-verdict/internal/protocol"
)

// ComponentName is the stable identity of this component.
const ComponentName = "sample-loading-and-curve-ingest"

// WellType identifies what a well holds.
type WellType string

const (
	WellSample            WellType = "sample"
	WellPositiveControl   WellType = "positive_control"
	WellNegativeControl   WellType = "negative_control"
	WellExtractionBlank   WellType = "extraction_blank"
	WellNoTemplateControl WellType = "no_template_control"
)

// Well is a single position on a plate.
type Well struct {
	Ref             protocol.WellRef `json:"ref"`
	Type            WellType         `json:"type"`
	TubeCode        string           `json:"tube_code,omitempty"`
	ExtractionBatch string           `json:"extraction_batch,omitempty"`
	PipetteBatch    string           `json:"pipette_batch,omitempty"`
	ReagentLot      string           `json:"reagent_lot,omitempty"`
}

// Plate is a fixed 96-well layout.
type Plate struct {
	ID    string `json:"id"`
	Wells []Well `json:"wells"`
}

// AssayBatch is the top-level experiment batch projection.
type AssayBatch struct {
	ID         string `json:"id"`
	Target     string `json:"target"`
	Generation int    `json:"generation"`
	Locked     bool   `json:"locked"`
	Plate      Plate  `json:"plate"`
}

// LoadRequest places a sample tube into a well.
type LoadRequest struct {
	OperationID string           `json:"operation_id"`
	TubeCode    string           `json:"tube_code"`
	Well        protocol.WellRef `json:"well"`
}

// InstrumentRun identifies one instrument run for a well and generation.
type InstrumentRun struct {
	ID         string           `json:"id"`
	Well       protocol.WellRef `json:"well"`
	Generation int              `json:"generation"`
}

// CurveChunk is one contiguous slice of an integer fluorescence curve.
type CurveChunk struct {
	RunID        string           `json:"run_id"`
	Well         protocol.WellRef `json:"well"`
	Generation   int              `json:"generation"`
	Seq          int              `json:"seq"`
	CycleStart   int              `json:"cycle_start"`
	CycleEnd     int              `json:"cycle_end"`
	Fluorescence []int64          `json:"fluorescence"`
	Digest       string           `json:"digest"`
	Complete     bool             `json:"complete"`
}

var (
	// ErrChunkOrder indicates the first chunk is not sequence 1.
	ErrChunkOrder = errors.New("chunk sequence must start at 1")
	// ErrChunkGap indicates a missing sequence number.
	ErrChunkGap = errors.New("chunk sequence gap")
	// ErrChunkOverlap indicates overlapping cycle ranges.
	ErrChunkOverlap = errors.New("chunk cycle overlap")
)

// ValidateChunkPrefix verifies that chunks form a contiguous, non-overlapping
// prefix starting at sequence 1.
func ValidateChunkPrefix(chunks []CurveChunk) error {
	if len(chunks) == 0 {
		return ErrChunkOrder
	}
	sorted := make([]CurveChunk, len(chunks))
	copy(sorted, chunks)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Seq < sorted[j].Seq })
	if sorted[0].Seq != 1 {
		return ErrChunkOrder
	}
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Seq != sorted[i-1].Seq+1 {
			return ErrChunkGap
		}
		if sorted[i].CycleStart <= sorted[i-1].CycleEnd {
			return ErrChunkOverlap
		}
	}
	return nil
}
