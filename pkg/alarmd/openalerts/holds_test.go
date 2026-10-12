// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"testing"
	"time"
)

// Holds is the membership question a close acts on, so it answers only
// where the answer is the link's: a calibrated set the Console has
// confirmed. Everywhere else it says it could not judge.
func TestHoldsJudgesOnlyACalibratedConfirmedSet(t *testing.T) {
	f := newSetFixture(t, PolicySelfMaintain, confirmedFacts(), "theirs-1", "theirs-2")
	if member, judged := f.cache.Holds(keyA, "theirs-1"); !member || !judged {
		t.Fatalf("Holds(member) = %v, %v; want a judged member", member, judged)
	}
	if count, judged := f.cache.MemberCount(keyA); count != 2 || !judged {
		t.Fatalf("MemberCount = %d, %v; want 2 judged", count, judged)
	}
	if !f.cache.Trusted() {
		t.Fatal("a confirmed set was not trusted")
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

	// Unconfirmed: the Console has not confirmed how the link keys alerts.
	facts := confirmedFacts()
	g := newSetFixture(t, PolicySelfMaintain, facts, "theirs-1")
	facts.set(true, false, true)
	if _, judged := g.cache.Holds(keyA, "theirs-1"); judged {
		t.Fatal("an unconfirmed set was judged")
	}
	if _, judged := g.cache.MemberCount(keyA); judged || g.cache.Trusted() {
		t.Fatal("an unconfirmed set was counted")
	}
	if g.cache.OwnEventSourceID() != "" {
		t.Fatal("a calibration that named no source produced one")
	}
	var nilCache *Cache
	if _, judged := nilCache.Holds(keyA, "x"); judged || nilCache.OwnEventSourceID() != "" || nilCache.Trusted() {
		t.Fatal("a nil copy answered")
	}
}
