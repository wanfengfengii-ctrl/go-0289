package verdict

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"edna-contamination-verdict/internal/protocol"
)

// ComputeClosure returns the sorted, minimal set of wells reachable from the
// seeds along the directed propagation edges. The closure always includes the
// seeds themselves and is ordered by plate, row then column.
func ComputeClosure(seeds []protocol.WellRef, edges []protocol.PropagationEdge) ContaminationSet {
	adj := map[string][]protocol.WellRef{}
	for _, e := range edges {
		adj[e.From.Key()] = append(adj[e.From.Key()], e.To)
	}
	seen := map[string]protocol.WellRef{}
	var queue []protocol.WellRef
	for _, s := range seeds {
		if _, ok := seen[s.Key()]; !ok {
			seen[s.Key()] = s
			queue = append(queue, s)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur.Key()] {
			if _, ok := seen[next.Key()]; ok {
				continue
			}
			seen[next.Key()] = next
			queue = append(queue, next)
		}
	}
	closure := make([]protocol.WellRef, 0, len(seen))
	for _, w := range seen {
		closure = append(closure, w)
	}
	SortWells(closure)
	seedCopy := append([]protocol.WellRef(nil), seeds...)
	SortWells(seedCopy)
	return ContaminationSet{
		Seeds:        seedCopy,
		Closure:      closure,
		SourceDigest: sourceDigest(seedCopy),
	}
}

// sourceDigest derives a stable content digest over a sorted seed list.
func sourceDigest(seeds []protocol.WellRef) string {
	h := sha256.New()
	for _, s := range seeds {
		fmt.Fprintf(h, "%s;", s.Key())
	}
	return hex.EncodeToString(h.Sum(nil))
}

// WellSet returns the closure as a set for fast membership checks.
func (c ContaminationSet) WellSet() map[string]bool {
	m := make(map[string]bool, len(c.Closure))
	for _, w := range c.Closure {
		m[w.Key()] = true
	}
	return m
}

// SortedKeys returns the closure keys in stable sorted order.
func (c ContaminationSet) SortedKeys() []string {
	keys := make([]string, 0, len(c.Closure))
	for _, w := range c.Closure {
		keys = append(keys, w.Key())
	}
	sort.Strings(keys)
	return keys
}
