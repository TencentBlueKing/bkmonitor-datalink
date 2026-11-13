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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The core case of decision-020 batch 8 (plan 5.4 B2): a round judged one
// group at a time, fed the stored groups in any order and with repeats, says
// exactly what the whole-read evaluation said - every verdict, every memory
// entry, every count, the outcome, the series and the mutation.
//
// The expectation is the whole-read code as it stood before the rewrite
// (judge_oracle_test.go), never the judgement itself. The inputs are drawn
// from every roster class, completeness, truncation, recovery, horizon,
// business, manual-close and memory shape the rules distinguish, and the draw
// is checked to have reached each path at least once: an equivalence over
// inputs that never took a path says nothing about that path.

const (
	equivalenceTime    = int64(1000)
	equivalencePeriod  = int64(60)
	equivalenceHorizon = int64(300)
	equivalenceCases   = 1500
)

type equivalenceClass int

const (
	classHistory equivalenceClass = iota
	classHistoryWholeItem
	classStatic
	classWholeItem
	classTargetPlan
	equivalenceClassCount
)

// equivalencePool is one class's groups: the key each is remembered under and
// a series that reports it.
type equivalencePool struct {
	keys   []string
	series []map[string]string
}

func equivalencePoolFor(t *testing.T, class equivalenceClass) equivalencePool {
	t.Helper()
	var pool equivalencePool
	for index := 1; index <= 6; index++ {
		var key string
		var series map[string]string
		switch class {
		case classTargetPlan:
			id := fmt.Sprintf("%d", 100+index)
			key, series = hostIDGroup(t, id).Key(), map[string]string{"bk_host_id": id}
		case classWholeItem:
			host := string(rune('a' + index - 1))
			key, series = closeHostGroup(host).Key(), map[string]string{"host": host}
		default:
			ip := fmt.Sprintf("192.0.2.%d", index)
			key = hostTargetGroup(HostIdentity{IP: ip, CloudID: "0"}).Key()
			series = map[string]string{HostIPDimension: ip, HostCloudDimension: "0"}
		}
		pool.keys = append(pool.keys, key)
		pool.series = append(pool.series, series)
	}
	return pool
}

// equivalenceMemoryStates are the stored shapes a group can be in, all of them
// at or before the record's present-as-of: seen last round, absent and seen
// before, absent and never seen, stopped by the horizon or a close, an absence
// old enough for the horizon to reach, and seen in this very round (a retry).
var equivalenceMemoryStates = []GroupMemory{
	{LastSeen: 940},
	{LastSeen: 700, FirstAbsent: 880},
	{FirstAbsent: 880},
	{LastSeen: 700, FirstAbsent: 600, SuppressedAt: 880},
	{FirstAbsent: 600, SuppressedAt: 880},
	{LastSeen: 100, FirstAbsent: 200},
	{FirstAbsent: 200},
	{LastSeen: equivalenceTime},
}

var equivalenceWholeItemStates = []GroupMemory{
	{FirstAbsent: 880},
	{FirstAbsent: 200},
	{FirstAbsent: 600, SuppressedAt: 880},
}

func equivalenceCase(t *testing.T, random *rand.Rand) PlanSlotInput {
	t.Helper()
	class := equivalenceClass(random.Intn(int(equivalenceClassCount)))
	pool := equivalencePoolFor(t, class)
	chance := func(percent int) bool { return random.Intn(100) < percent }

	input := planSlotInput(execution.NoDataMemorySnapshot{})
	input.NoData = &contract.NoDataConfigV1{Continuous: 3, Level: 2, RecoveryDisabled: chance(25)}
	input.Scope, input.KnownHosts, input.HostsResolved = nil, nil, false
	switch class {
	case classHistory:
		input.NoData.AggDimension = []string{HostIPDimension, HostCloudDimension}
	case classHistoryWholeItem:
		input.NoData.AggDimension = []string{}
	case classStatic:
		input.NoData.AggDimension = []string{HostIPDimension, HostCloudDimension}
		var declared, known []string
		for index := 1; index <= 6; index++ {
			host := fmt.Sprintf("192.0.2.%d|0", index)
			if chance(60) {
				declared = append(declared, host)
			}
			if chance(80) {
				known = append(known, host)
			}
		}
		if len(declared) == 0 {
			// An include list has at least one key, or it is not one.
			declared = append(declared, "192.0.2.6|0")
		}
		input.Scope, input.KnownHosts, input.HostsResolved = hostScope(declared...), knownHosts(known...), chance(92)
	case classWholeItem:
		input.NoData.AggDimension = []string{"host"}
		input.Scope = hostScope("192.0.2.1|0")
	case classTargetPlan:
		input.NoData.AggDimension = []string{"bk_host_id"}
		input.Plan = hostTargetPlan("101")
		var members []string
		for index := 1; index <= 6; index++ {
			if chance(50) {
				members = append(members, fmt.Sprintf("%d", 100+index))
			}
		}
		switch draw := random.Intn(10); {
		case draw == 0:
			input.TargetResolution = nil
		case draw == 1:
			input.TargetResolution = &TargetResolution{State: TargetResolutionUnavailable, Members: members}
		case draw == 2:
			input.TargetResolution = &TargetResolution{State: TargetResolutionIncomplete, Members: members}
		default:
			input.TargetResolution = &TargetResolution{State: TargetResolutionComplete, Members: members}
		}
	}

	if chance(70) {
		input.Completeness = execution.CompletenessFull
	} else {
		input.Completeness = execution.CompletenessPartial
	}
	input.AnswerTruncated = chance(15)
	if chance(50) {
		input.TrackingHorizonSeconds = equivalenceHorizon
	}

	var groups []execution.NoDataGroupMemory
	remember := func(key string, entry GroupMemory) {
		groups = append(groups, execution.NoDataGroupMemory{
			GroupKey: key, LastSeen: entry.LastSeen, FirstAbsent: entry.FirstAbsent, SuppressedAt: entry.SuppressedAt,
		})
	}
	input.OutOfBusiness = map[string]struct{}{}
	for index, key := range pool.keys {
		if chance(60) {
			remember(key, equivalenceMemoryStates[random.Intn(len(equivalenceMemoryStates))])
		}
		if class == classHistoryWholeItem {
			continue
		}
		if chance(40) {
			input.Series = append(input.Series, pool.series[index])
		}
		if chance(15) {
			input.OutOfBusiness[key] = struct{}{}
		}
	}
	if class == classHistoryWholeItem && chance(50) {
		input.Series = append(input.Series, map[string]string{"anything": "1"})
	}
	if class != classHistoryWholeItem && chance(20) {
		input.Series = append(input.Series, map[string]string{"unrelated": "1"})
	}
	if chance(35) {
		remember(WholeItemGroup().Key(), equivalenceWholeItemStates[random.Intn(len(equivalenceWholeItemStates))])
	}
	if chance(5) {
		if chance(50) {
			remember(unparseableKey, GroupMemory{LastSeen: 940})
		} else {
			remember(unparseableKey, GroupMemory{LastSeen: 700, FirstAbsent: 880})
		}
	}
	sort.Slice(groups, func(left, right int) bool { return groups[left].GroupKey < groups[right].GroupKey })

	if chance(30) {
		identity := contract.MonitorOutputIdentity{DynamicDimensions: true}
		closes := &ManualCloses{
			Fingerprint: func(group Group) (string, error) { return GroupFingerprint("7", "2", identity, group) },
			ClosedAt:    map[string]int64{},
		}
		for _, group := range groups {
			parsed, ok := ParseGroupKey(group.GroupKey)
			if !ok || group.FirstAbsent == 0 || !chance(60) {
				continue
			}
			fingerprint, err := GroupFingerprint("7", "2", identity, parsed)
			if err != nil {
				t.Fatal(err)
			}
			closes.ClosedAt[fingerprint] = 900
		}
		input.ManualCloses = closes
	}

	snapshot := execution.NoDataMemorySnapshot{
		Identity: planSlotIdentity(), Status: execution.NoDataMemoryMissing,
		Representation: execution.NoDataRepresentationNone,
	}
	if len(groups) != 0 || chance(70) {
		presentAsOf := int64(0)
		for _, group := range groups {
			if group.LastSeen > presentAsOf {
				presentAsOf = group.LastSeen
			}
		}
		if presentAsOf == 0 && chance(50) {
			presentAsOf = 940
		}
		snapshot = execution.NoDataMemorySnapshot{
			Identity: planSlotIdentity(), Status: execution.NoDataMemoryFound,
			Representation: execution.NoDataRepresentationPerGroup,
			MarkerRevision: 4, SchemaVersion: execution.WrittenNoDataMemorySchema,
			PersistedApplyVersion:   execution.ApplyVersion{StateApplyEpoch: 6, EvaluationTime: 940, SlotDigest: "slot"},
			PersistedMutationDigest: "digest", LastScheduleRevision: "revision-1",
			RosterVersion: []string{"", "stale"}[random.Intn(2)], PresentAsOf: presentAsOf, Groups: groups,
		}
		if len(groups) == 0 && chance(50) {
			snapshot.TrackingExhaustedAt = 900
		}
	}
	input.Snapshot = snapshot
	return input
}

type namedEquivalenceCase struct {
	name  string
	input PlanSlotInput
}

// namedEquivalenceCases are the paths a random draw reaches too rarely to be
// left to it, written out once each.
func namedEquivalenceCases(t *testing.T) []namedEquivalenceCase {
	t.Helper()
	history := func(groups ...execution.NoDataGroupMemory) PlanSlotInput {
		input := planSlotInput(storedSnapshot(t, 940, groups...))
		input.Scope, input.KnownHosts, input.HostsResolved = nil, nil, false
		input.TrackingHorizonSeconds = equivalenceHorizon
		return input
	}
	host := func(ip string) string { return hostTargetGroup(HostIdentity{IP: ip, CloudID: "0"}).Key() }
	whole := WholeItemGroup().Key()

	exhausted := history(
		execution.NoDataGroupMemory{GroupKey: host("192.0.2.1"), LastSeen: 100, FirstAbsent: 200},
		execution.NoDataGroupMemory{GroupKey: host("192.0.2.2"), LastSeen: 700, FirstAbsent: 600, SuppressedAt: 880},
	)
	carried := history()
	carried.Snapshot.PresentAsOf, carried.Snapshot.TrackingExhaustedAt = 0, 900
	wholeAbsent := history(execution.NoDataGroupMemory{GroupKey: whole, FirstAbsent: 880})
	wholeExpired := history(execution.NoDataGroupMemory{GroupKey: whole, FirstAbsent: 200})
	wholeClosed := history(
		execution.NoDataGroupMemory{GroupKey: whole, FirstAbsent: 880},
		execution.NoDataGroupMemory{GroupKey: host("192.0.2.1"), LastSeen: 940},
	)
	emptyTarget := planSlotInput(storedSnapshot(t, 940,
		execution.NoDataGroupMemory{GroupKey: hostIDGroup(t, "101").Key(), LastSeen: 700, FirstAbsent: 880},
		execution.NoDataGroupMemory{GroupKey: whole, FirstAbsent: 880},
	))
	emptyTarget.NoData.AggDimension = []string{"bk_host_id"}
	emptyTarget.Scope, emptyTarget.Plan = nil, hostTargetPlan("101")
	emptyTarget.TargetResolution = &TargetResolution{State: TargetResolutionComplete}
	recoveryHeld := planSlotInput(storedSnapshot(t, 940,
		execution.NoDataGroupMemory{GroupKey: host("192.0.2.1"), LastSeen: 700, FirstAbsent: 880},
	), presentSeries())
	recoveryHeld.NoData.RecoveryDisabled = true
	closed := planSlotInput(storedSnapshot(t, 940,
		execution.NoDataGroupMemory{GroupKey: host("192.0.2.2"), LastSeen: 700, FirstAbsent: 880},
	), presentSeries())
	closed.ManualCloses = closesNaming(t, host("192.0.2.2"), 900)
	unresolved := planSlotInput(storedSnapshot(t, 940), presentSeries())
	unresolved.HostsResolved, unresolved.KnownHosts = false, nil
	return []namedEquivalenceCase{
		{"history exhausted by the horizon", exhausted},
		{"exhaustion carried over an empty memory", carried},
		{"whole item absent", wholeAbsent},
		{"whole item absence expired", wholeExpired},
		{"whole item absence closed by a history roster", wholeClosed},
		{"confirmed-empty target plan", emptyTarget},
		{"recovery disabled", recoveryHeld},
		{"manual close", closed},
		{"hosts unresolved", unresolved},
	}
}

// shuffledWithRepeats is the stored groups as a scan may return them: in no
// particular order, and with some fields returned twice.
func shuffledWithRepeats(groups []execution.NoDataGroupMemory, random *rand.Rand) []execution.NoDataGroupMemory {
	fed := append([]execution.NoDataGroupMemory(nil), groups...)
	for index, group := range groups {
		if index == 0 || random.Intn(100) < 30 {
			fed = append(fed, group)
		}
	}
	random.Shuffle(len(fed), func(left, right int) { fed[left], fed[right] = fed[right], fed[left] })
	return fed
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// nextRoundSnapshot is the record a store holds once it applied a round's
// mutation, read back the way the next round reads it.
func nextRoundSnapshot(
	loaded execution.NoDataMemorySnapshot, mutation *execution.PlanNoDataMutation,
) execution.NoDataMemorySnapshot {
	memory := make(map[string]execution.NoDataGroupMemory, len(loaded.Groups))
	if !mutation.ReplacesWholeRecord() {
		for _, group := range loaded.Groups {
			memory[group.GroupKey] = group
		}
	}
	for _, group := range mutation.Set {
		if group.Absent == nil {
			memory[group.GroupKey] = execution.NoDataGroupMemory{GroupKey: group.GroupKey, LastSeen: mutation.PresentAsOf}
			continue
		}
		memory[group.GroupKey] = execution.NoDataGroupMemory{
			GroupKey: group.GroupKey, LastSeen: group.Absent.LastSeen, FirstAbsent: group.Absent.FirstAbsent,
			SuppressedAt: group.Absent.SuppressedAt,
		}
	}
	for _, key := range mutation.Del {
		delete(memory, key)
	}
	groups := make([]execution.NoDataGroupMemory, 0, len(memory))
	for _, group := range memory {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(left, right int) bool { return groups[left].GroupKey < groups[right].GroupKey })
	return execution.NoDataMemorySnapshot{
		Identity: mutation.Identity, Status: execution.NoDataMemoryFound,
		Representation: execution.NoDataRepresentationPerGroup, MarkerRevision: mutation.ExpectedMarkerRevision + 1,
		SchemaVersion: mutation.SchemaVersion, PersistedApplyVersion: mutation.ApplyVersion,
		PersistedMutationDigest: mutation.MutationDigest, LastScheduleRevision: mutation.ScheduleRevision,
		RosterVersion: mutation.RosterVersion, PresentAsOf: mutation.PresentAsOf,
		TrackingExhaustedAt: mutation.TrackingExhaustedAt, Groups: groups,
	}
}

// equivalenceReach counts the paths the draw took, so the test can refuse a
// draw that never took one.
type equivalenceReach map[string]int

func (reach equivalenceReach) note(result PlanSlotResult, err error, slot AbsenceResult) {
	if err != nil {
		reach["error"]++
		return
	}
	reach["outcome "+string(result.Outcome)]++
	facts := result.Facts
	for name, count := range map[string]uint64{
		"absent": facts.Absent, "unavailable": facts.Unavailable, "expired": facts.Expired,
		"suppressed": facts.Suppressed, "manual closed": facts.ManualClosed, "dropped": facts.Dropped,
	} {
		if count > 0 {
			reach[name]++
		}
	}
	if len(slot.Closing) != 0 {
		reach["closing"]++
	}
	if slot.WholeItemPresent {
		reach["whole item present"]++
	}
	if slot.Verdicts[WholeItemGroup().Key()] == VerdictAnomaly {
		reach["whole item absent"]++
	}
	if slot.Roster.Source == RosterTargetPlan && len(slot.Roster.Groups) == 0 {
		reach["empty target plan"]++
	}
	switch {
	case result.Mutation != nil && result.Mutation.TrackingExhaustedAt == equivalenceTime:
		reach["exhausted this round"]++
	case result.Mutation != nil:
		reach["written"]++
	case result.Outcome == OutcomeEvaluated:
		reach["nothing moved"]++
	}
}

// The whole round: EvaluatePlanSlot, and the same round fed in a shuffled order
// with repeats, against the whole-read round.
func TestPlanJudgeFedInAnyOrderWithRepeatsMatchesTheWholeRead(t *testing.T) {
	reach := equivalenceReach{}
	named := namedEquivalenceCases(t)
	for seed := 0; seed < equivalenceCases+len(named); seed++ {
		random := rand.New(rand.NewSource(int64(seed)))
		var input PlanSlotInput
		label := fmt.Sprintf("seed %d", seed)
		if seed < len(named) {
			input, label = named[seed].input, named[seed].name
		} else {
			input = equivalenceCase(t, random)
		}
		for round := 0; round < 2; round++ {
			want, wantErr := oracleEvaluatePlanSlot(input)
			assertPlanSlotMatches(t, fmt.Sprintf("%s round %d", label, round), input, random, want, wantErr)
			slot := assertSlotMatches(t, fmt.Sprintf("%s round %d", label, round), input, random)
			reach.note(want, wantErr, slot)
			if wantErr != nil || want.Mutation == nil {
				break
			}
			// The same Slot again against what the first one wrote: the retry
			// that finds the memory already moved, and most often nothing to do.
			input.Snapshot = nextRoundSnapshot(input.Snapshot, want.Mutation)
		}
	}
	for _, path := range []string{
		"error", "absent", "unavailable", "expired", "suppressed", "manual closed", "dropped", "closing",
		"whole item present", "whole item absent", "empty target plan", "exhausted this round", "written",
		"nothing moved",
		"outcome " + string(OutcomeEvaluated), "outcome " + string(OutcomeSkippedQueryNotFull),
		"outcome " + string(OutcomeSkippedAnswerTruncated), "outcome " + string(OutcomeSkippedHostsUnresolved),
		"outcome " + string(OutcomeSkippedTargetSelectorUnavailable),
		"outcome " + string(OutcomeSkippedTargetMembersDropped),
	} {
		if reach[path] == 0 {
			t.Errorf("no drawn case reached %q; the equivalence says nothing about it (reached: %v)", path, reach)
		}
	}
}

func assertPlanSlotMatches(
	t *testing.T, name string, input PlanSlotInput, random *rand.Rand, want PlanSlotResult, wantErr error,
) {
	t.Helper()
	got, err := EvaluatePlanSlot(input)
	if errorText(err) != errorText(wantErr) || !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: EvaluatePlanSlot() = %+v, %v\nwant %+v, %v", name, got, err, want, wantErr)
	}

	judge, err := NewPlanJudge(input)
	if err != nil {
		if errorText(err) != errorText(wantErr) {
			t.Fatalf("%s: NewPlanJudge() error = %v, want %v", name, err, wantErr)
		}
		return
	}
	for _, group := range shuffledWithRepeats(input.Snapshot.Groups, random) {
		if err = judge.Feed(group); err != nil {
			break
		}
	}
	shuffled := PlanSlotResult{}
	if err == nil {
		shuffled, err = judge.planSlotResult(input.Snapshot.Groups)
	}
	if errorText(err) != errorText(wantErr) || !reflect.DeepEqual(shuffled, want) {
		t.Fatalf("%s: fed shuffled with repeats = %+v, %v\nwant %+v, %v", name, shuffled, err, want, wantErr)
	}
}

// assertSlotMatches holds the whole result - the UNAVAILABLE verdicts, the
// memory as a map, the roster - to the whole read, and the delta to what
// actually moved.
func assertSlotMatches(t *testing.T, name string, input PlanSlotInput, random *rand.Rand) AbsenceResult {
	t.Helper()
	if input.Snapshot.Status != execution.NoDataMemoryFound && input.Snapshot.Status != execution.NoDataMemoryMissing {
		return AbsenceResult{}
	}
	memory := oracleLoadedMemory(input.Snapshot)
	slotInput := SlotInput{
		Plan:           &contract.EvaluationPlanV2{NoData: input.NoData, TargetScope: input.Scope, TargetPlan: input.Plan},
		EvaluationTime: input.EvaluationTime, PeriodSeconds: input.PeriodSeconds, Completeness: input.Completeness,
		Series: input.Series, KnownHosts: input.KnownHosts, HostsResolved: input.HostsResolved,
		OutOfBusiness: input.OutOfBusiness, TargetResolution: input.TargetResolution,
		AnswerTruncated: input.AnswerTruncated, Memory: memory,
		TrackingHorizonSeconds: input.TrackingHorizonSeconds, TrackingExhaustedAt: input.Snapshot.TrackingExhaustedAt,
		ManualCloses: input.ManualCloses,
	}
	want, wantOutcome, wantErr := oracleEvaluateSlot(slotInput)
	got, outcome, err := EvaluateSlot(slotInput)
	if outcome != wantOutcome || errorText(err) != errorText(wantErr) || !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: EvaluateSlot() = %+v, %s, %v\nwant %+v, %s, %v", name, got, outcome, err, want, wantOutcome, wantErr)
	}

	judge, outcome, err := newSlotJudge(slotInput)
	if err != nil || judge == nil {
		return want
	}
	keys := make([]string, 0, len(input.Snapshot.Groups))
	for _, group := range shuffledWithRepeats(input.Snapshot.Groups, random) {
		keys = append(keys, group.GroupKey)
	}
	shuffled, err := judgeAbsenceResult(judge, memory, keys)
	if errorText(err) != errorText(wantErr) || !reflect.DeepEqual(shuffled, want) {
		t.Fatalf("%s: judged shuffled with repeats = %+v, %v\nwant %+v, %v", name, shuffled, err, want, wantErr)
	}
	if wantErr != nil {
		return want
	}
	assertRosterVersionIsTheFold(t, name, input, got)

	// An explicitly stated roster, the other way in: the one the Slot built,
	// and a history roster the memory did not derive.
	explicit := AbsenceInput{
		EvaluationTime: input.EvaluationTime, PeriodSeconds: input.PeriodSeconds, Completeness: input.Completeness,
		Present: ProjectSeries(input.Series, input.NoData.AggDimension).Groups, Roster: want.Roster, Memory: memory,
		OutOfBusiness: input.OutOfBusiness, TrackingHorizonSeconds: input.TrackingHorizonSeconds,
		TrackingExhaustedAt: input.Snapshot.TrackingExhaustedAt, RecoveryDisabled: input.NoData.RecoveryDisabled,
		AnswerTruncated: input.AnswerTruncated,
	}
	if random.Intn(2) == 0 {
		stated := Roster{Version: "stated", Source: RosterHistory, Groups: map[string]Group{}}
		for key := range explicit.Present {
			if group, ok := ParseGroupKey(key); ok && random.Intn(2) == 0 {
				stated.Groups[key] = group
			}
		}
		for key := range memory {
			if group, ok := ParseGroupKey(key); ok && random.Intn(2) == 0 {
				stated.Groups[key] = group
			}
		}
		explicit.Roster = stated
	}
	wantExplicit := oracleEvaluate(explicit)
	if gotExplicit := Evaluate(explicit); !reflect.DeepEqual(gotExplicit, wantExplicit) {
		t.Fatalf("%s: Evaluate() = %+v\nwant %+v", name, gotExplicit, wantExplicit)
	}
	explicitShuffled, err := judgeAbsenceResult(newAbsenceJudge(explicit, false, nil), memory, keys)
	if err != nil || !reflect.DeepEqual(explicitShuffled, wantExplicit) {
		t.Fatalf("%s: stated roster judged shuffled with repeats = %+v, %v\nwant %+v", name, explicitShuffled, err, wantExplicit)
	}

	assertDeltaIsWhatMoved(t, name, input, want, outcome)
	return want
}

// assertRosterVersionIsTheFold holds the roster version to the v2 fold over
// the deduplicated key set, which the oracle cannot state: it is the old code,
// and its own derivation was v1 (judge_oracle_test.go). A history set is read
// off the stored groups here - every key with a last-seen time except the
// whole item, once however often the scan returned it - and every other set
// is the roster the Slot built.
func assertRosterVersionIsTheFold(t *testing.T, name string, input PlanSlotInput, got AbsenceResult) {
	t.Helper()
	keys := map[string]struct{}{}
	if got.Roster.Source == RosterHistory {
		for _, group := range input.Snapshot.Groups {
			if group.GroupKey != WholeItemGroup().Key() && group.LastSeen != 0 {
				keys[group.GroupKey] = struct{}{}
			}
		}
	} else {
		for key := range got.Roster.Groups {
			keys[key] = struct{}{}
		}
	}
	var fold rosterVersionFold
	for key := range keys {
		fold.add(key)
	}
	want, err := fold.version(got.Roster.Source)
	if err != nil {
		t.Fatal(err)
	}
	if got.Facts.RosterVersion != want || got.Roster.Version != want {
		t.Fatalf("%s: roster version = %q (facts %q), want the fold over the %d deduplicated keys %q",
			name, got.Roster.Version, got.Facts.RosterVersion, len(keys), want)
	}
}

// assertDeltaIsWhatMoved: the loaded record with the delta applied is the
// memory the whole read stores, and nothing in the delta is a group that did
// not move.
func assertDeltaIsWhatMoved(t *testing.T, name string, input PlanSlotInput, want AbsenceResult, outcome SlotOutcome) {
	t.Helper()
	judge, err := NewPlanJudge(input)
	if err != nil {
		t.Fatalf("%s: NewPlanJudge() = %v", name, err)
	}
	for _, group := range input.Snapshot.Groups {
		if err := judge.Feed(group); err != nil {
			t.Fatalf("%s: Feed() = %v", name, err)
		}
	}
	judgement, err := judge.Finish()
	if err != nil {
		t.Fatalf("%s: Finish() = %v", name, err)
	}
	if judgement.Outcome != outcome || !reflect.DeepEqual(judgement.Facts, want.Facts) {
		t.Fatalf("%s: judgement = %s %+v, want %s %+v", name, judgement.Outcome, judgement.Facts, outcome, want.Facts)
	}
	if !judgement.Judged {
		return
	}
	loaded := make(map[string]GroupMemory, len(input.Snapshot.Groups))
	for _, group := range input.Snapshot.Groups {
		loaded[group.GroupKey] = GroupMemory{
			LastSeen: group.LastSeen, FirstAbsent: group.FirstAbsent, SuppressedAt: group.SuppressedAt,
		}
	}
	applied := copyGroupMemory(loaded)
	for index, change := range judgement.Delta {
		if index > 0 && judgement.Delta[index-1].Key >= change.Key {
			t.Fatalf("%s: delta %+v is not in key order", name, judgement.Delta)
		}
		before, wasStored := loaded[change.Key]
		if change.Kept {
			if wasStored && before == change.Memory {
				t.Fatalf("%s: delta holds %+v, which did not move", name, change)
			}
			applied[change.Key] = change.Memory
			continue
		}
		if !wasStored {
			t.Fatalf("%s: delta forgets %q, which was never stored", name, change.Key)
		}
		delete(applied, change.Key)
	}
	if !reflect.DeepEqual(storedGroups(applied), storedGroups(want.Memory)) {
		t.Fatalf("%s: loaded plus delta = %+v, want the whole read's %+v", name, storedGroups(applied), storedGroups(want.Memory))
	}
}

// The scan fixture of the design (2.7, K2): one present field and one absent
// field each returned twice. The second copy is ignored even when it carries a
// different value - the first read is the one judged - and the round is the
// whole read's.
func TestPlanJudgeIgnoresAFieldTheScanReturnsTwice(t *testing.T) {
	present := hostTargetGroup(HostIdentity{IP: "192.0.2.1", CloudID: "0"}).Key()
	absent := hostTargetGroup(HostIdentity{IP: "192.0.2.2", CloudID: "0"}).Key()
	input := planSlotInput(storedSnapshot(t, 940,
		execution.NoDataGroupMemory{GroupKey: present, LastSeen: 940},
		execution.NoDataGroupMemory{GroupKey: absent, LastSeen: 700, FirstAbsent: 880},
	), presentSeries())
	input.Scope, input.KnownHosts, input.HostsResolved = nil, nil, false
	want, err := oracleEvaluatePlanSlot(input)
	if err != nil {
		t.Fatal(err)
	}
	judge, err := NewPlanJudge(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []execution.NoDataGroupMemory{
		{GroupKey: absent, LastSeen: 700, FirstAbsent: 880},
		{GroupKey: present, LastSeen: 940},
		{GroupKey: present, LastSeen: 1},
		{GroupKey: absent, LastSeen: 700, FirstAbsent: 1},
	} {
		if err := judge.Feed(group); err != nil {
			t.Fatal(err)
		}
	}
	got, err := judge.planSlotResult(input.Snapshot.Groups)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("fed with repeats = %+v, %v\nwant %+v", got, err, want)
	}
	if got.Facts.Absent != 1 || got.Facts.Expected != 2 {
		t.Fatalf("facts = %+v, want the repeated absent group counted once and the roster of two", got.Facts)
	}
}

// A finished round takes no more groups: one fed after the counts are settled
// would be judged into nothing.
func TestPlanJudgeRefusesAGroupAfterItFinished(t *testing.T) {
	judge, err := NewPlanJudge(planSlotInput(storedSnapshot(t, 940)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := judge.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := judge.Feed(execution.NoDataGroupMemory{GroupKey: "late", LastSeen: 940}); err == nil {
		t.Fatal("a group fed after Finish was accepted")
	}
	if _, err := judge.Finish(); err == nil {
		t.Fatal("a round finished twice was accepted")
	}
}
