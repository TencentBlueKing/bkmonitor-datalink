// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import "testing"

// The pair is what can be acted on. A CONFIG_REJECTED strategy has stopped
// detecting and a STALE_CONFIG one is still running its last good Plan, so the
// same reason means two different things depending on which it is, and a count
// by reason alone cannot separate them.
func TestWithheldCountsThePairAndNotJustTheReason(t *testing.T) {
	composition := ComposeCatalog(Catalog{Dispositions: []ObjectDisposition{
		{SourceID: "1", Disposition: DispositionConfigRejected, Reason: "NO_DATA_CONFIG_INVALID"},
		{SourceID: "2", Disposition: DispositionStaleConfig, Reason: "NO_DATA_CONFIG_INVALID"},
		{SourceID: "3", Disposition: DispositionConfigRejected, Reason: "PLAN_INVALID"},
		{SourceID: "4", Disposition: DispositionAccepted},
	}})
	for key, want := range map[WithheldKey]int{
		{Disposition: DispositionConfigRejected, Reason: "NO_DATA_CONFIG_INVALID"}: 1,
		{Disposition: DispositionStaleConfig, Reason: "NO_DATA_CONFIG_INVALID"}:    1,
		{Disposition: DispositionConfigRejected, Reason: "PLAN_INVALID"}:           1,
	} {
		if got := composition.Withheld[key]; got != want {
			t.Fatalf("Withheld[%+v] = %d, want %d", key, got, want)
		}
	}
	// An accepted object was not withheld, so it is in no pair at all.
	for key := range composition.Withheld {
		if key.Disposition == DispositionAccepted {
			t.Fatalf("Withheld carries %+v; an accepted object was not withheld", key)
		}
	}
	// The partition adds up against the disposition counts it came from.
	var withheld int
	for _, count := range composition.Withheld {
		withheld += count
	}
	if want := 3; withheld != want {
		t.Fatalf("withheld total = %d, want %d", withheld, want)
	}
}

// The reason reaches the count as it was attached, with no list deciding
// whether it is allowed to. A list would have to be exactly the set this
// package writes, and the first version of it held twenty of the forty-odd -
// which would have filed most of a deployment's rejected objects under a label
// naming nothing while a guard reported all was well.
func TestWithheldCarriesTheReasonAsItWasAttached(t *testing.T) {
	const unusual = "A_REASON_NOBODY_LISTED"
	composition := ComposeCatalog(Catalog{Dispositions: []ObjectDisposition{
		{SourceID: "1", Disposition: DispositionConfigRejected, Reason: unusual},
	}})
	key := WithheldKey{Disposition: DispositionConfigRejected, Reason: unusual}
	if got := composition.Withheld[key]; got != 1 {
		t.Fatalf("Withheld[%+v] = %d, want the reason counted under its own name", key, got)
	}
	for held, count := range composition.Withheld {
		if count == 0 {
			// A pair the composition publishes at zero, not one this round
			// produced. It cannot be a rewritten reason: nothing was counted
			// under it. See AlwaysReportedWithheld.
			continue
		}
		if held.Reason != unusual {
			t.Fatalf("Withheld carries %+v with %d objects; the reason was rewritten on the way in",
				held, count)
		}
	}
}

// The withheld pairs must add up to the objects that were not accepted.
//
// This is what makes a zero readable. On the deployment this was written for,
// every pair reads zero because nothing is being withheld for a reason this
// release introduced - and a family whose expected value is zero cannot tell
// "nothing is wrong" from "nothing was computed". Read against the disposition
// counts it partitions, it can: the same pass over the same dispositions
// produces both, so a zero that adds up is a zero that was computed.
func TestWithheldPartitionsTheObjectsThatWereNotAccepted(t *testing.T) {
	composition := ComposeCatalog(Catalog{Dispositions: []ObjectDisposition{
		{SourceID: "1", Disposition: DispositionAccepted},
		{SourceID: "2", Disposition: DispositionAccepted},
		{SourceID: "3", Disposition: DispositionConfigRejected, Reason: "NO_DATA_CONFIG_INVALID"},
		{SourceID: "4", Disposition: DispositionUnsupported, Reason: "ALGORITHM_NOT_MIGRATED"},
		{SourceID: "5", Disposition: DispositionStaleConfig, Reason: "PLAN_INVALID"},
		{SourceID: "6", Disposition: Disposition("A_DISPOSITION_NOBODY_LISTED"), Reason: "PLAN_INVALID"},
	}})

	var objects, accepted int
	for disposition, count := range composition.Objects {
		objects += count
		if disposition == DispositionAccepted {
			accepted += count
		}
	}
	var withheld int
	for _, count := range composition.Withheld {
		withheld += count
	}
	if want := objects - accepted; withheld != want {
		t.Fatalf("withheld total = %d, want %d: the pairs do not partition the objects that were not "+
			"accepted, so a zero in the family cannot be read against the disposition counts", withheld, want)
	}

	// Including the object whose disposition this build does not name: it is
	// folded to other in both counts, so the partition holds rather than
	// quietly losing a member on one side of the comparison.
	key := WithheldKey{Disposition: DispositionOther, Reason: "PLAN_INVALID"}
	if got := composition.Withheld[key]; got != 1 {
		t.Fatalf("Withheld[%+v] = %d, want the unnamed disposition folded the same way Objects folds it", key, got)
	}
}

// The two reasons whose zero is a claim are published even when nothing was
// withheld under them.
//
// Most pairs are not pre-created and should not be: the cross product of every
// disposition with every reason is mostly combinations that cannot happen. But
// "no strategy in this deployment asks for a Snapshot kept longer than we keep
// one" is something an operator acts on, and read off an absent series it is
// indistinguishable from "this build does not produce that reason" -- which is
// exactly the state the deployment was in the day before the reason existed.
// Both of these arrived with the change that withholds a Plan instead of
// refusing the whole Catalog, and both are the acceptance reading for it.
func TestTheRetentionWithheldReasonsArePublishedAtZero(t *testing.T) {
	composition := ComposeCatalog(Catalog{Dispositions: []ObjectDisposition{
		{SourceID: "1", Disposition: DispositionAccepted, Reason: "ACCEPTED"},
	}})

	for _, key := range AlwaysReportedWithheld {
		count, published := composition.Withheld[key]
		if !published {
			t.Fatalf("Withheld has no pair %+v. Its zero is what says no object was withheld for that "+
				"reason; absent, it says nothing at all and reads the same as a build that cannot "+
				"produce it", key)
		}
		if count != 0 {
			t.Fatalf("Withheld[%+v] = %d on a Catalog that withheld nothing", key, count)
		}
	}

	// And a pre-created pair does not become a second count when something is
	// withheld under it: the zero is a starting point, not an extra object.
	withheld := ComposeCatalog(Catalog{Dispositions: []ObjectDisposition{
		{SourceID: "1", Disposition: DispositionUnsupported, Reason: "SNAPSHOT_RETENTION_INSUFFICIENT"},
	}})
	key := WithheldKey{Disposition: DispositionUnsupported, Reason: "SNAPSHOT_RETENTION_INSUFFICIENT"}
	if got := withheld.Withheld[key]; got != 1 {
		t.Fatalf("Withheld[%+v] = %d, want the one object that was withheld", key, got)
	}
	var total int
	for _, count := range withheld.Withheld {
		total += count
	}
	if total != 1 {
		t.Fatalf("withheld total = %d, want 1: the pre-created pairs must not add objects to the "+
			"partition, or it stops adding up against catalog_objects", total)
	}
}

// The composition names the strategies the source listed this round, once
// each and sorted, beside the withheld records: every disposition but the
// two that mean the source no longer lists the strategy. A reader keeping
// the strategies the source dropped needs the round's listed set to tell
// "listed again" from "gone" -- a removed strategy has no disposition at all
// the round after -- and a strategy back in the list that compiles no Plan
// this round is listed all the same.
func TestCompositionNamesTheListedStrategiesOnceEach(t *testing.T) {
	composition := ComposeCatalog(Catalog{Dispositions: []ObjectDisposition{
		{SourceID: "s-2", Scope: "STRATEGY", Disposition: DispositionAccepted},
		{SourceID: "s-2", Scope: "PLAN", Disposition: DispositionAccepted},
		{SourceID: "s-1", Scope: "STRATEGY", Disposition: DispositionAccepted},
		{SourceID: "s-3", Scope: "STRATEGY", Disposition: DispositionPendingRemoval, Reason: "REMOVED_FROM_ACTIVE_SET"},
		{SourceID: "s-4", Scope: "STRATEGY", Disposition: DispositionRemoved, Reason: "ABSENT_FROM_ACTIVE_SET"},
		{SourceID: "s-5", Scope: "STRATEGY", Disposition: DispositionStaleConfig, Reason: "LEVEL_INVALID"},
		{SourceID: "s-6", Scope: "PLAN", Disposition: DispositionConfigRejected, Reason: "PLAN_INVALID"},
	}})
	if got := composition.ListedStrategies; len(got) != 4 || got[0] != "s-1" || got[1] != "s-2" || got[2] != "s-5" || got[3] != "s-6" {
		t.Fatalf("listed = %v, want s-1, s-2, s-5, s-6 once each and sorted: the graced and the removed are not listed, the stale and the rejected are", got)
	}
}
