package verdict

import (
	"errors"

	"edna-contamination-verdict/internal/protocol"
)

// Sentinel errors for retest generation and review/final arbitration.
var (
	ErrRetestConflict      = errors.New("retest conflict")
	ErrReviewerDuplicate   = errors.New("duplicate reviewer")
	ErrReviewerUnqualified = errors.New("reviewer not qualified")
	ErrDigestMismatch      = errors.New("evidence digest mismatch")
	ErrDecisionExists      = errors.New("final decision already exists")
	ErrNotReady            = errors.New("batch not ready for decision")
)

// Qualification levels. Only operator and scientist may sign a final decision.
const (
	QualificationTrainee   = "trainee"
	QualificationOperator  = "operator"
	QualificationScientist = "scientist"
)

// IsQualified reports whether a qualification level may sign a decision.
func IsQualified(q string) bool {
	return q == QualificationOperator || q == QualificationScientist
}

// FindRetest locates an existing retest generation that was created for the
// given parent digest at the given generation number.
func FindRetest(retests []Generation, number int, parentDigest string) (Generation, bool) {
	for _, g := range retests {
		if g.Number == number && g.ParentDigest == parentDigest {
			return g, true
		}
	}
	return Generation{}, false
}

// NewGeneration builds the next retest generation that reopens only the
// affected wells. The parent digest identifies the defect summary that
// triggered the retest.
func NewGeneration(number int, parentDigest string, reopened []protocol.WellRef) Generation {
	wells := append([]protocol.WellRef(nil), reopened...)
	SortWells(wells)
	return Generation{
		Number:        number,
		ReopenedWells: wells,
		ParentDigest:  parentDigest,
	}
}
