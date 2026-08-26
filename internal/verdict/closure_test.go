package verdict

import (
	"testing"

	"edna-contamination-verdict/internal/protocol"
)

func w(r, c int) protocol.WellRef { return protocol.WellRef{Plate: "P", Row: r, Col: c} }

func TestComputeClosureTransitiveAndSorted(t *testing.T) {
	edges := []protocol.PropagationEdge{
		{From: w(1, 1), To: w(1, 2)},
		{From: w(1, 2), To: w(2, 1)},
		{From: w(3, 1), To: w(3, 2)},
	}
	set := ComputeClosure([]protocol.WellRef{w(1, 1)}, edges)
	want := []protocol.WellRef{w(1, 1), w(1, 2), w(2, 1)}
	if len(set.Closure) != 3 {
		t.Fatalf("expected 3 closure wells, got %v", set.Closure)
	}
	for i := range want {
		if set.Closure[i] != want[i] {
			t.Fatalf("closure[%d] = %v want %v", i, set.Closure[i], want[i])
		}
	}
}

func TestComputeClosureUnreachableExcluded(t *testing.T) {
	edges := []protocol.PropagationEdge{
		{From: w(1, 1), To: w(1, 2)},
	}
	set := ComputeClosure([]protocol.WellRef{w(1, 1)}, edges)
	if len(set.Closure) != 2 {
		t.Fatalf("expected 2 closure wells, got %d", len(set.Closure))
	}
}

func TestFindRetestAndGeneration(t *testing.T) {
	g := NewGeneration(2, "digest-1", []protocol.WellRef{w(2, 1), w(1, 1)})
	if g.Number != 2 || g.ParentDigest != "digest-1" {
		t.Fatalf("unexpected generation %+v", g)
	}
	// Reopened wells must be sorted.
	if g.ReopenedWells[0] != w(1, 1) || g.ReopenedWells[1] != w(2, 1) {
		t.Fatalf("reopened wells not sorted: %v", g.ReopenedWells)
	}
	if _, ok := FindRetest([]Generation{g}, 2, "digest-1"); !ok {
		t.Fatal("expected to find retest")
	}
	if _, ok := FindRetest([]Generation{g}, 3, "digest-1"); ok {
		t.Fatal("should not find retest at wrong generation")
	}
}

func TestValidateReviewRules(t *testing.T) {
	existing := []Review{{ReviewerID: "alice", Qualification: QualificationOperator, Digest: "d"}}
	if err := ValidateReview(existing, Review{ReviewerID: "alice", Qualification: QualificationOperator, Digest: "d"}, "d"); err != ErrReviewerDuplicate {
		t.Fatalf("want duplicate, got %v", err)
	}
	if err := ValidateReview(existing, Review{ReviewerID: "bob", Qualification: QualificationTrainee, Digest: "d"}, "d"); err != ErrReviewerUnqualified {
		t.Fatalf("want unqualified, got %v", err)
	}
	if err := ValidateReview(existing, Review{ReviewerID: "bob", Qualification: QualificationOperator, Digest: "x"}, "d"); err != ErrDigestMismatch {
		t.Fatalf("want digest mismatch, got %v", err)
	}
	if err := ValidateReview(existing, Review{ReviewerID: "bob", Qualification: QualificationScientist, Digest: "d"}, "d"); err != nil {
		t.Fatalf("want success, got %v", err)
	}
}

func TestDecideSingleWriter(t *testing.T) {
	var existing *FinalDecision
	d1, ok, err := Decide(existing, FinalDecision{Type: FinalRelease, Seq: 1})
	if err != nil || !ok {
		t.Fatalf("first decide should win, ok=%v err=%v", ok, err)
	}
	existing = &d1
	if _, ok, err := Decide(existing, FinalDecision{Type: FinalVoid, Seq: 2}); err != ErrDecisionExists || ok {
		t.Fatalf("second decide should fail, ok=%v err=%v", ok, err)
	}
}

func TestHasQuorumRequiresTwoQualified(t *testing.T) {
	reviews := []Review{
		{ReviewerID: "a", Qualification: QualificationOperator, Digest: "d"},
		{ReviewerID: "b", Qualification: QualificationScientist, Digest: "d"},
	}
	if !HasQuorum(reviews, "d") {
		t.Fatal("expected quorum")
	}
	reviews = []Review{{ReviewerID: "a", Qualification: QualificationOperator, Digest: "d"}}
	if HasQuorum(reviews, "d") {
		t.Fatal("one reviewer is not a quorum")
	}
}
