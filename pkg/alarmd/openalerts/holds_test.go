// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"testing"
	"time"
)

// Holds is the membership question a close acts on, so it answers only
// where the answer is the link's: a calibrated set that is not disjoint from
// this process's own sends. Everywhere else it says it could not judge.
func TestHoldsJudgesOnlyACalibratedSetThatIsNotDisjoint(t *testing.T) {
	f := newDisjointFixture(t, PolicySelfMaintain, "theirs-1", "theirs-2")
	if member, judged := f.cache.Holds(keyA, "theirs-1"); !member || !judged {
		t.Fatalf("Holds(member) = %v, %v; want a judged member", member, judged)
	}
	if count, judged := f.cache.MemberCount(keyA); count != 2 || !judged {
		t.Fatalf("MemberCount = %d, %v; want 2 judged", count, judged)
	}
	if f.cache.Disjoint() {
		t.Fatal("a set carrying members was read as disjoint")
	}
	if member, judged := f.cache.Holds(keyA, "elsewhere"); member || !judged {
		t.Fatalf("Holds(non-member) = %v, %v; want a judged non-member", member, judged)
	}
	if _, judged := f.cache.Holds(StrategyKey{TenantID: tenant, StrategyID: "untracked"}, "theirs-1"); judged {
		t.Fatal("a strategy with no calibrated set was judged")
	}
	lookups := f.cache.Stats().Lookups
	var total uint64
	for _, count := range lookups {
		total += count
	}
	if total != 0 {
		t.Fatalf("Holds counted gate lookups %v; it is not the gate", lookups)
	}

	// Past the calibration bound the set is no longer the link's word.
	f.c.advance(time.Hour * 24)
	if _, judged := f.cache.Holds(keyA, "theirs-1"); judged {
		t.Fatal("a set whose calibration aged out was judged")
	}
	if _, judged := f.cache.MemberCount(keyA); judged {
		t.Fatal("a set whose calibration aged out was counted")
	}

	// Disjoint: the sets carry none of this process's alerts.
	g := newDisjointFixture(t, PolicySelfMaintain, "theirs-1")
	ours := sentFingerprints(DisjointMinimum)
	g.send(ours...)
	g.reread(SentConfirmAfter + time.Second)
	if !g.cache.Stats().Disjoint {
		t.Fatal("fixture did not reach the disjoint state")
	}
	if _, judged := g.cache.Holds(keyA, "theirs-1"); judged {
		t.Fatal("a disjoint set was judged")
	}
	if _, judged := g.cache.MemberCount(keyA); judged || !g.cache.Disjoint() {
		t.Fatal("a disjoint set was counted")
	}
	if g.cache.OwnEventSourceID() != "" {
		t.Fatal("a calibration that named no source produced one")
	}
	var nilCache *Cache
	if _, judged := nilCache.Holds(keyA, "x"); judged || nilCache.OwnEventSourceID() != "" || nilCache.Disjoint() {
		t.Fatal("a nil copy answered")
	}
}
