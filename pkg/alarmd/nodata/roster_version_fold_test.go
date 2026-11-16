// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The v2 roster version (rosterVersionDomain): one order-free fold for every
// source, fed through the judgement's own deduplication.

// foldHostKeys is n host group keys, each distinct.
func foldHostKeys(n int) []string {
	keys := make([]string, 0, n)
	for index := 0; index < n; index++ {
		keys = append(keys, hostTargetGroup(HostIdentity{IP: fmt.Sprintf("192.0.2.%d", index+1), CloudID: "0"}).Key())
	}
	return keys
}

// rosterOfKeys is a roster naming keys; a key named twice is one group, as a
// roster's map holds it. The groups are zero: the version reads keys only.
func rosterOfKeys(source RosterSource, keys ...string) Roster {
	roster := Roster{Source: source, Groups: make(map[string]Group, len(keys))}
	for _, key := range keys {
		roster.Groups[key] = Group{}
	}
	return roster
}

func sortedKeys(keys []string) []string {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	return sorted
}

func mustRosterVersion(t *testing.T, roster Roster) string {
	t.Helper()
	version, err := rosterVersion(roster)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

// historyPlanSlotInput is the fixture Plan with no target, so its expected set
// is the history roster derived from the memory, and a FULL round with no data.
func historyPlanSlotInput(snapshot execution.NoDataMemorySnapshot) PlanSlotInput {
	input := planSlotInput(snapshot)
	input.Scope, input.KnownHosts, input.HostsResolved = nil, nil, false
	return input
}

// A history roster scanned in any order, with fields the scan returned more
// than once, has the version of its set: the judgement folds each key once,
// through the same seen bit and judged set it judges by. The stored whole-item
// group is in the memory and not in the set.
func TestTheRosterVersionOfAScannedHistoryIsTheFoldOfItsSet(t *testing.T) {
	keys := foldHostKeys(12)
	groups := make([]execution.NoDataGroupMemory, 0, len(keys)+1)
	for _, key := range keys {
		groups = append(groups, execution.NoDataGroupMemory{GroupKey: key, LastSeen: 700, FirstAbsent: 880})
	}
	groups = append(groups, execution.NoDataGroupMemory{GroupKey: WholeItemGroup().Key(), FirstAbsent: 880})
	want := mustRosterVersion(t, rosterOfKeys(RosterHistory, keys...))

	for seed := int64(0); seed < 50; seed++ {
		random := rand.New(rand.NewSource(seed))
		input := historyPlanSlotInput(storedSnapshot(t, 940, groups...))
		judge, err := NewPlanJudge(input)
		if err != nil {
			t.Fatal(err)
		}
		fed := shuffledWithRepeats(groups, random)
		if len(fed) <= len(groups) {
			t.Fatalf("seed %d: fixture: the scan returned no field twice", seed)
		}
		for _, group := range fed {
			if err := judge.Feed(group); err != nil {
				t.Fatal(err)
			}
		}
		judgement, err := judge.Finish()
		if err != nil {
			t.Fatal(err)
		}
		if !judgement.Judged || judgement.Facts.Expected != uint64(len(keys)) {
			t.Fatalf("seed %d: judgement = %+v, want a judged round expecting the %d keys once each",
				seed, judgement, len(keys))
		}
		if judgement.RosterVersion != want || judgement.Facts.RosterVersion != want {
			t.Fatalf("seed %d: roster version = %q (facts %q), want the fold of the set %q",
				seed, judgement.RosterVersion, judgement.Facts.RosterVersion, want)
		}
	}
}

// A built roster is folded the same way: a static target naming its hosts in
// another order and some twice is the set of its hosts.
func TestTheRosterVersionOfABuiltRosterIsTheFoldOfItsSet(t *testing.T) {
	hosts := []string{"192.0.2.3|0", "192.0.2.1|0", "192.0.2.2|0", "192.0.2.1|0", "192.0.2.3|0"}
	roster, err := BuildRoster(RosterRequest{
		AggDimension: []string{HostIPDimension, HostCloudDimension},
		Scope:        hostScope(hosts...),
		KnownHosts:   knownHosts(hosts...),
	})
	if err != nil {
		t.Fatal(err)
	}
	keys := foldHostKeys(3)
	if want := mustRosterVersion(t, rosterOfKeys(RosterTargetStatic, keys[2], keys[0], keys[1])); roster.Version != want {
		t.Fatalf("static roster version = %q, want the fold of its three hosts %q", roster.Version, want)
	}
	// Order is not in it, and neither is how often the caller named a key.
	if reversed := mustRosterVersion(t, rosterOfKeys(RosterTargetStatic, keys[1], keys[0], keys[2], keys[0])); reversed != roster.Version {
		t.Fatalf("the same set in another order gave %q, want %q", reversed, roster.Version)
	}
}

// The source is in the version: two sources over the same keys, and over no
// keys, never share one.
func TestTheRosterVersionSeparatesSources(t *testing.T) {
	for _, keys := range [][]string{nil, foldHostKeys(1), foldHostKeys(4)} {
		seen := map[string]RosterSource{}
		for _, source := range []RosterSource{RosterHistory, RosterTargetStatic, RosterTargetPlan, RosterWhole} {
			version := mustRosterVersion(t, rosterOfKeys(source, keys...))
			if other, taken := seen[version]; taken {
				t.Fatalf("%d keys: %s and %s share the version %q", len(keys), other, source, version)
			}
			seen[version] = source
		}
	}
}

// A v1 value never equals a v2 value for the same set: the domains differ.
// That is what makes the first full round after the upgrade a roster that
// moved rather than one the old record already stated.
func TestTheRosterVersionV1NeverEqualsV2(t *testing.T) {
	for _, keys := range [][]string{nil, foldHostKeys(1), foldHostKeys(5)} {
		sorted := sortedKeys(keys)
		for _, source := range []RosterSource{RosterHistory, RosterTargetStatic, RosterTargetPlan, RosterWhole} {
			v1, err := oracleRosterVersionV1(source, sorted)
			if err != nil {
				t.Fatal(err)
			}
			if v2 := mustRosterVersion(t, rosterOfKeys(source, keys...)); v1 == v2 || v1 == "" || v2 == "" {
				t.Fatalf("%s over %d keys: v1 %q, v2 %q, want two different values", source, len(keys), v1, v2)
			}
		}
	}
}

// A partial round decided nothing about the expected set, so the stored
// version goes forward as the record holds it - a v1 value as well as a v2
// one, and neither replaced by what this round would have derived.
func TestAPartialRoundCarriesTheStoredRosterVersionForward(t *testing.T) {
	keys := foldHostKeys(2)
	v1, err := oracleRosterVersionV1(RosterTargetStatic, sortedKeys(keys))
	if err != nil {
		t.Fatal(err)
	}
	// A v2 value other than this round's: one host fewer.
	otherV2 := mustRosterVersion(t, rosterOfKeys(RosterTargetStatic, keys[0]))
	if otherV2 == planSlotRosterVersion(t) {
		t.Fatal("fixture: the stored v2 value must differ from this round's")
	}
	for name, stored := range map[string]string{"v1": v1, "v2": otherV2} {
		t.Run(name, func(t *testing.T) {
			snapshot := storedSnapshot(t, 940, execution.NoDataGroupMemory{GroupKey: keys[0], LastSeen: 940})
			snapshot.RosterVersion = stored
			input := planSlotInput(snapshot, presentSeries())
			input.Completeness = execution.CompletenessPartial

			result, err := EvaluatePlanSlot(input)
			if err != nil {
				t.Fatal(err)
			}
			if result.Outcome != OutcomeSkippedQueryNotFull || result.Mutation == nil {
				t.Fatalf("result = %s %+v, want a partial round that writes the present group's last-seen",
					result.Outcome, result.Mutation)
			}
			if result.Mutation.RosterVersion != stored {
				t.Fatalf("a partial round wrote roster version %q, want the stored %q carried forward",
					result.Mutation.RosterVersion, stored)
			}
		})
	}
}

// The upgrade's one write per Plan: a record the old build wrote, whose groups
// this round leaves exactly as they are, is rewritten as its header alone. The
// delta is empty - no group field set or removed - and the header names the
// v2 roster version and the memory digest that goes with it.
func TestTheFirstRoundAfterTheUpgradeRewritesOnlyTheHeader(t *testing.T) {
	first, err := EvaluatePlanSlot(planSlotInput(
		execution.NoDataMemorySnapshot{Identity: planSlotIdentity(), Status: execution.NoDataMemoryMissing,
			Representation: execution.NoDataRepresentationNone},
		presentSeries(),
	))
	if err != nil || first.Mutation == nil {
		t.Fatalf("fixture: %+v, %v", first.Mutation, err)
	}
	stored := storedAfter(t, execution.NoDataMemorySnapshot{Representation: execution.NoDataRepresentationNone}, first.Mutation)
	// The fixture's expected set is its two hosts; the old build stated it as
	// v1, over the keys in order.
	v1, err := oracleRosterVersionV1(RosterTargetStatic, sortedKeys(foldHostKeys(2)))
	if err != nil {
		t.Fatal(err)
	}
	stored.RosterVersion = v1
	input := planSlotInput(stored, presentSeries())
	// The digest a header stating v1 has for the same memory: what this round
	// has to move off.
	asStored, err := execution.BuildPlanNoDataMutation(execution.PlanNoDataMemoryUpdate{
		Identity: stored.Identity, DerivedFrom: execution.NoDataRepresentationPerGroup,
		LoadedApplyVersion: stored.PersistedApplyVersion, ExpectedMarkerRevision: stored.MarkerRevision,
		ApplyVersion: input.ApplyVersion, ScheduleRevision: input.ScheduleRevision,
		RosterVersion: v1, PresentAsOf: stored.PresentAsOf, Memory: stored.Groups,
		Loaded: stored.Groups, LoadedPresentAsOf: stored.PresentAsOf,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := EvaluatePlanSlot(input)
	if err != nil {
		t.Fatal(err)
	}
	mutation := result.Mutation
	if mutation == nil {
		t.Fatal("a v1 record was not rewritten on the first full round")
	}
	if mutation.ReplacesWholeRecord() || len(mutation.Set) != 0 || len(mutation.Del) != 0 {
		t.Fatalf("the upgrade rewrote groups: replace %v, set %+v, del %v; want the header alone",
			mutation.ReplacesWholeRecord(), mutation.Set, mutation.Del)
	}
	if mutation.RosterVersion != planSlotRosterVersion(t) || mutation.RosterVersion == v1 {
		t.Fatalf("header roster version = %q, want this round's v2 %q", mutation.RosterVersion, planSlotRosterVersion(t))
	}
	if mutation.MemoryDigest == asStored.MemoryDigest {
		t.Fatalf("header memory digest %s is the one the v1 header states", mutation.MemoryDigest)
	}
	if mutation.MemoryDigest != first.Mutation.MemoryDigest || mutation.GroupCount != first.Mutation.GroupCount {
		t.Fatalf("header names memory %s of %d groups, want the unchanged memory under v2, %s of %d",
			mutation.MemoryDigest, mutation.GroupCount, first.Mutation.MemoryDigest, first.Mutation.GroupCount)
	}
}

// The judgement keeps no history key beyond its one deduplication set.
//
// This is structural, and stated as such: every field of absenceJudge that can
// hold a string is named here with why it does, so a field added to hold keys
// fails this until it is justified. The history roster's own field holds no
// string at all. Then, behaviourally, a round over a large history leaves no
// expected group in the judgement or in its summary: the keys it judged are in
// the judged set, which is the deduplication the scan needs, and nowhere else.
func TestThePlanJudgeHoldsNoHistoryKeyBeyondTheDeduplication(t *testing.T) {
	allowed := map[string]string{
		"roster":      "the expected set when it is known before the read; empty for a derived history",
		"present":     "this round's data, which is the input",
		"foreign":     "the hosts of another business this round, an input",
		"closes":      "the manual closes, an input",
		"presentSeen": "the deduplication for present groups",
		"judged":      "the deduplication for every other group",
		"facts":       "the Plan-level counts, whose only strings are the roster source and version",
	}
	judgeType := reflect.TypeOf(absenceJudge{})
	for index := 0; index < judgeType.NumField(); index++ {
		field := judgeType.Field(index)
		if _, ok := allowed[field.Name]; !ok && canHoldString(field.Type, map[reflect.Type]bool{}) {
			t.Errorf("absenceJudge.%s (%s) can hold a key and is not one the judgement is allowed", field.Name, field.Type)
		}
	}
	if history, ok := judgeType.FieldByName("history"); !ok || canHoldString(history.Type, map[reflect.Type]bool{}) {
		t.Errorf("the history roster's fold must exist and hold no string: %+v", history)
	}

	keys := foldHostKeys(200)
	groups := make([]execution.NoDataGroupMemory, 0, len(keys))
	for _, key := range keys {
		groups = append(groups, execution.NoDataGroupMemory{GroupKey: key, LastSeen: 700, FirstAbsent: 880})
	}
	judge, err := NewPlanJudge(historyPlanSlotInput(storedSnapshot(t, 940, groups...)))
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if err := judge.Feed(group); err != nil {
			t.Fatal(err)
		}
	}
	absence := judge.absence
	if len(absence.roster.Groups) != 0 || len(absence.judged) != len(keys) || absence.history.size() != uint64(len(keys)) {
		t.Fatalf("after %d history groups: roster holds %d, judged %d, fold %d; want 0, %d, %d",
			len(keys), len(absence.roster.Groups), len(absence.judged), absence.history.size(), len(keys), len(keys))
	}
	summary, _, err := absence.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if summary.Roster.Groups != nil || summary.Facts.Expected != uint64(len(keys)) {
		t.Fatalf("summary roster holds %d groups, expected %d; want no groups and %d expected",
			len(summary.Roster.Groups), summary.Facts.Expected, len(keys))
	}
}

// canHoldString says whether a value of t can contain a string.
func canHoldString(t reflect.Type, visiting map[reflect.Type]bool) bool {
	if visiting[t] {
		return false
	}
	visiting[t] = true
	switch t.Kind() {
	case reflect.String, reflect.Interface:
		return true
	case reflect.Map:
		return canHoldString(t.Key(), visiting) || canHoldString(t.Elem(), visiting)
	case reflect.Slice, reflect.Array, reflect.Pointer, reflect.Chan:
		return canHoldString(t.Elem(), visiting)
	case reflect.Struct:
		for index := 0; index < t.NumField(); index++ {
			if canHoldString(t.Field(index).Type, visiting) {
				return true
			}
		}
	case reflect.Func:
		return true
	}
	return false
}
