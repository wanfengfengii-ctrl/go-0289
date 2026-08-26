package protocol

import (
	"errors"
	"sort"
)

// Sentinel validation errors. The transport layer maps each to a stable API
// error code; the domain packages keep only typed Go errors.
var (
	ErrInvalidScale         = errors.New("invalid scale factor")
	ErrInvalidBaseline      = errors.New("invalid baseline range")
	ErrInvalidWindow        = errors.New("invalid crossing window")
	ErrInvalidPositiveRange = errors.New("invalid positive control range")
	ErrMissingReplicates    = errors.New("sample missing replicate wells")
	ErrDuplicateTube        = errors.New("duplicate tube code")
	ErrControlShortage      = errors.New("control shortage")
	ErrInvalidEdge          = errors.New("invalid propagation edge")
	ErrWellOutOfBounds      = errors.New("well out of plate bounds")
	ErrWellReused           = errors.New("well assigned more than once")
	ErrUnknownProtocol      = errors.New("unknown protocol")
)

// TubePlacement maps one sample tube to exactly one well.
type TubePlacement struct {
	TubeCode string  `json:"tube_code"`
	Well     WellRef `json:"well"`
}

// SampleSpec describes one sample and the replicate tubes that occupy its
// replicate wells. A sample must have exactly ReplicateCount tubes, each tube
// in its own well.
type SampleSpec struct {
	ReplicateGroup string          `json:"replicate_group"`
	Tubes          []TubePlacement `json:"tubes"`
}

// ControlSpec places a single control into a well.
type ControlSpec struct {
	Kind ControlKind `json:"kind"`
	Well WellRef     `json:"well"`
}

// LayoutSpec is the plate layout portion of a protocol definition.
type LayoutSpec struct {
	PlateID  string        `json:"plate_id"`
	Rows     int           `json:"rows"`
	Cols     int           `json:"cols"`
	Samples  []SampleSpec  `json:"samples"`
	Controls []ControlSpec `json:"controls"`
}

// ProtocolSpec is the full experiment rule definition supplied before lock.
type ProtocolSpec struct {
	ID             string            `json:"id"`
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
	Layout         LayoutSpec        `json:"layout"`
}

// WellAssignment is the resolved role of a single well after locking.
type WellAssignment struct {
	Ref            WellRef     `json:"ref"`
	IsSample       bool        `json:"is_sample"`
	Control        ControlKind `json:"control,omitempty"`
	TubeCode       string      `json:"tube_code,omitempty"`
	ReplicateGroup string      `json:"replicate_group,omitempty"`
}

// LockResult is produced by Lock and carries the immutable snapshot plus the
// resolved well assignments.
type LockResult struct {
	Snapshot    ProtocolSnapshot `json:"snapshot"`
	Assignments []WellAssignment `json:"assignments"`
}

// Lock validates the supplied protocol and, on success, returns the immutable
// snapshot and resolved assignments. It never mutates the input.
func Lock(spec ProtocolSpec) (LockResult, error) {
	if spec.Scale <= 0 {
		return LockResult{}, ErrInvalidScale
	}
	if spec.BaselineStart < 0 || spec.BaselineEnd <= spec.BaselineStart {
		return LockResult{}, ErrInvalidBaseline
	}
	if spec.Window < 1 {
		return LockResult{}, ErrInvalidWindow
	}
	if spec.PositiveMin > spec.PositiveMax {
		return LockResult{}, ErrInvalidPositiveRange
	}
	if spec.ReplicateCount < 1 {
		return LockResult{}, ErrMissingReplicates
	}
	rows, cols := spec.Layout.Rows, spec.Layout.Cols
	if rows <= 0 || cols <= 0 {
		return LockResult{}, ErrWellOutOfBounds
	}

	inBounds := func(w WellRef) bool {
		return w.Row >= 1 && w.Row <= rows && w.Col >= 1 && w.Col <= cols
	}

	// Resolve sample assignments and enforce replicate counts and uniqueness.
	assignments := make([]WellAssignment, 0, rows*cols)
	used := map[string]string{}  // well key -> "sample"/"control"
	tubes := map[string]string{} // tube -> group

	for _, s := range spec.Layout.Samples {
		if len(s.Tubes) != spec.ReplicateCount {
			return LockResult{}, ErrMissingReplicates
		}
		if s.ReplicateGroup == "" {
			return LockResult{}, ErrMissingReplicates
		}
		for _, tp := range s.Tubes {
			if tp.TubeCode == "" {
				return LockResult{}, ErrDuplicateTube
			}
			if _, dup := tubes[tp.TubeCode]; dup {
				return LockResult{}, ErrDuplicateTube
			}
			tubes[tp.TubeCode] = s.ReplicateGroup
			if !inBounds(tp.Well) {
				return LockResult{}, ErrWellOutOfBounds
			}
			if _, ok := used[tp.Well.Key()]; ok {
				return LockResult{}, ErrWellReused
			}
			used[tp.Well.Key()] = "sample"
			assignments = append(assignments, WellAssignment{
				Ref:            tp.Well,
				IsSample:       true,
				TubeCode:       tp.TubeCode,
				ReplicateGroup: s.ReplicateGroup,
			})
		}
	}

	// Controls must include at least one positive and one negative control.
	var positive, negative int
	for _, c := range spec.Layout.Controls {
		if !inBounds(c.Well) {
			return LockResult{}, ErrWellOutOfBounds
		}
		if _, ok := used[c.Well.Key()]; ok {
			return LockResult{}, ErrWellReused
		}
		used[c.Well.Key()] = "control"
		switch c.Kind {
		case PositiveControl:
			positive++
		case NegativeControl:
			negative++
		case ExtractionBlank, NoTemplateControl:
			// extraction and no-template controls are negative-acting.
		default:
			return LockResult{}, ErrControlShortage
		}
		assignments = append(assignments, WellAssignment{
			Ref:     c.Well,
			Control: c.Kind,
		})
	}
	if positive < 1 || negative < 1 {
		return LockResult{}, ErrControlShortage
	}

	// Every propagation edge must reference wells present in the layout.
	for _, e := range spec.Edges {
		if _, ok := used[e.From.Key()]; !ok {
			return LockResult{}, ErrInvalidEdge
		}
		if _, ok := used[e.To.Key()]; !ok {
			return LockResult{}, ErrInvalidEdge
		}
	}

	snapshot := ProtocolSnapshot{
		Target:         spec.Target,
		Scale:          spec.Scale,
		Threshold:      spec.Threshold,
		BaselineStart:  spec.BaselineStart,
		BaselineEnd:    spec.BaselineEnd,
		Window:         spec.Window,
		PositiveMin:    spec.PositiveMin,
		PositiveMax:    spec.PositiveMax,
		ReplicateCount: spec.ReplicateCount,
		ReagentLot:     spec.ReagentLot,
		Edges:          append([]PropagationEdge(nil), spec.Edges...),
	}
	snapshot.Digest = snapshot.ComputeDigest()

	sort.Slice(assignments, func(i, j int) bool {
		if assignments[i].Ref.Plate != assignments[j].Ref.Plate {
			return assignments[i].Ref.Plate < assignments[j].Ref.Plate
		}
		if assignments[i].Ref.Row != assignments[j].Ref.Row {
			return assignments[i].Ref.Row < assignments[j].Ref.Row
		}
		return assignments[i].Ref.Col < assignments[j].Ref.Col
	})

	return LockResult{Snapshot: snapshot, Assignments: assignments}, nil
}
