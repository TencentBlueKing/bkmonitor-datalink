// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// sharedGroupStrategy is a cutover fixture strategy on strategy 1001's query,
// firing on the backend's 5 at a threshold of 1, with edit applied. Every
// strategy built this way shares one Query Group.
func sharedGroupStrategy(t *testing.T, id int64, updateTime int64, edit func(strategy map[string]any)) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var strategy map[string]any
	if err := json.Unmarshal(raw, &strategy); err != nil {
		t.Fatal(err)
	}
	strategy["id"], strategy["update_time"] = id, updateTime
	item := strategy["items"].([]any)[0].(map[string]any)
	item["id"] = id % 1000
	item["query_configs"].([]any)[0].(map[string]any)["agg_interval"] = 60
	for _, algorithm := range item["algorithms"].([]any) {
		algorithm.(map[string]any)["config"] = []any{[]any{map[string]any{"method": "gte", "threshold": 1}}}
	}
	for _, detect := range strategy["detects"].([]any) {
		detect.(map[string]any)["trigger_config"].(map[string]any)["check_window"] = cutoverStallTriggerWindow
	}
	if edit != nil {
		edit(strategy)
	}
	encoded, err := json.Marshal(strategy)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// installSharedGroup stores the strategies of the shared Query Group, with
// strategy 1002 on its own table as installCutoverStallStrategies stored it;
// a strategy left out is deleted. It writes as the strategy cache's writer
// does: the change signal moved, and its statement that it holds every
// strategy's last good document, so a strategy that left the set is gone at
// once rather than kept through the absence grace.
func installSharedGroup(t *testing.T, ctx context.Context, client *redis.Client, strategies map[int64][]byte) {
	t.Helper()
	ids := []int64{1002}
	for id, encoded := range strategies {
		ids = append(ids, id)
		if err := client.Set(ctx, "alarm-config.strategy_"+strconv.FormatInt(id, 10), encoded, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	encoded, err := json.Marshal(ids)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "alarm-config.strategy_ids", string(encoded), 0).Err(); err != nil {
		t.Fatal(err)
	}
	lastUpdated := int64(1)
	if current, err := client.Get(ctx, "alarm-config.last_updated").Int64(); err == nil {
		lastUpdated = current + 1
	}
	digest := sha256.Sum256(encoded)
	statement, err := json.Marshal(map[string]any{"version": 1, "hold_last_good": true, "last_updated": lastUpdated,
		"strategy_ids_sha256": hex.EncodeToString(digest[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "alarm-config.publication_semantics", statement, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "alarm-config.last_updated", strconv.FormatInt(lastUpdated, 10), 0).Err(); err != nil {
		t.Fatal(err)
	}
}

// thresholdAt is an edit that moves every condition's threshold.
func thresholdAt(value int) func(strategy map[string]any) {
	return func(strategy map[string]any) {
		for _, algorithm := range strategy["items"].([]any)[0].(map[string]any)["algorithms"].([]any) {
			algorithm.(map[string]any)["config"] = []any{[]any{map[string]any{"method": "gte", "threshold": value}}}
		}
	}
}

// renumberLevels moves every Level to id 2: no Level is common to both
// versions, so the Plan's state is not carried (none_no_common_level) and
// it warms up whole.
func renumberLevels(strategy map[string]any) {
	for _, algorithm := range strategy["items"].([]any)[0].(map[string]any)["algorithms"].([]any) {
		algorithm.(map[string]any)["level"] = 2
	}
	for _, detect := range strategy["detects"].([]any) {
		detect.(map[string]any)["level"] = 2
	}
}

// startSharedGroupFixture is the cutover fixture with strategies 1001 and
// 1003 (A and B) in one Query Group, both firing on every Slot, strategy 1002
// in its own: the first Slot completed, the cursor at the second grid point.
func startSharedGroupFixture(t *testing.T) *cutoverStallFixture {
	t.Helper()
	return openCutoverFixtureSharing(t, nil, observability.Discard(observability.ComponentRuntime),
		func(ctx context.Context, client *redis.Client, _ config.Config) {
			installSharedGroup(t, ctx, client, map[int64][]byte{
				1001: sharedGroupStrategy(t, 1001, 1725000000, nil),
				1003: sharedGroupStrategy(t, 1003, 1725000000, nil),
			})
		}, nil, nil, 2)
}

// eventsFor is the events a strategy wrote for a Slot.
func (fixture *cutoverStallFixture) eventsFor(slot execution.EvaluationTime, strategy string) []contract.TriggerEventV1 {
	var events []contract.TriggerEventV1
	for _, event := range fixture.events.snapshot() {
		if event.EvaluationTime == int64(slot) && event.PlanRef.StrategyID == strategy {
			events = append(events, event)
		}
	}
	return events
}

// stallInFlight begins the Slot at slot and stops it in its read: its
// unfinished projection stays persisted under the Segment it was frozen in.
func (fixture *cutoverStallFixture) stallInFlight(ctx context.Context, slot execution.EvaluationTime) {
	fixture.t.Helper()
	fixture.clock.Store(firstRoundAtMillis(fixture.cfg, fixture.queryGroup, int64(slot), 60))
	roundCtx, stopRound := context.WithCancel(ctx)
	fixture.stopInRead.Store(&stopRound)
	result, attempted, err := fixture.runner.RunOne(roundCtx)
	stopRound()
	if !attempted || result.Completed || !errors.Is(err, context.Canceled) {
		fixture.t.Fatalf("stopped Slot RunOne = (%+v, %t, %v), want an attempted, uncompleted Slot stopped in its read", result, attempted, err)
	}
}

// cutOverAt publishes what the strategy source holds, cut over at the
// second given, and wants the Segment open before it closed there.
func (fixture *cutoverStallFixture) cutOverAt(ctx context.Context, boundary execution.EvaluationTime) {
	fixture.t.Helper()
	fixture.clock.Store(int64(boundary) * 1000)
	for round := 0; round < 2; round++ {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			fixture.t.Fatalf("leader round %d: %v", round, err)
		}
	}
	closed, err := fixture.production.dependencies.Catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, boundary-1)
	if err != nil || closed.Segment.End == nil || *closed.Segment.End != boundary {
		fixture.t.Fatalf("the Segment before %d after the cutover = (%+v, %v), want it closed there", boundary, closed.Segment, err)
	}
}

// runUntilEnded runs the Query Group's rounds, each at its next ready
// instant, until slot has ended, and returns how it ended.
func (fixture *cutoverStallFixture) runUntilEnded(ctx context.Context, slot execution.EvaluationTime) string {
	fixture.t.Helper()
	for attempt := 0; attempt < 40; attempt++ {
		// A round stopped or cancelled leaves a line with no completion kind:
		// the Slot has not ended.
		if ended, ok := fixture.slotEndings()[slot]; ok && !strings.HasPrefix(ended, "/") {
			return ended
		}
		if nextAt := fixture.runner.NextReadyAt(); nextAt.After(fixture.now()) {
			fixture.clock.Store(nextAt.UnixMilli() + 1)
		}
		if _, attempted, err := fixture.runner.RunOne(ctx); err != nil {
			fixture.t.Fatalf("RunOne for Slot %d: %v", slot, err)
		} else if !attempted {
			// Nothing due: on to the Slot's first round, a second at a time
			// once there.
			fixture.clock.Store(max(fixture.now().UnixMilli()+1000, firstRoundAtMillis(fixture.cfg, fixture.queryGroup, int64(slot), 60)))
		}
	}
	fixture.t.Fatalf("Slot %d did not end; endings %v", slot, fixture.slotEndings())
	return ""
}

// wantEvents wants each strategy named to have written events for the Slot,
// and each one in none to have written none.
func (fixture *cutoverStallFixture) wantEvents(slot execution.EvaluationTime, some []string, none []string) {
	fixture.t.Helper()
	for _, strategy := range some {
		if events := fixture.eventsFor(slot, strategy); len(events) == 0 {
			fixture.t.Fatalf("strategy %s wrote no event for Slot %d; endings %v", strategy, slot, fixture.slotEndings())
		}
	}
	for _, strategy := range none {
		if events := fixture.eventsFor(slot, strategy); len(events) != 0 {
			fixture.t.Fatalf("strategy %s wrote %d events for Slot %d, want none", strategy, len(events), slot)
		}
	}
}

// An edit to one strategy of a shared Query Group lands while a Slot is in
// flight. The Slot, dated before the cutover's boundary, completes under the
// configuration it was frozen with - for both strategies, the edited one and
// the one beside it, which stayed - instead of being thrown away as if both
// had left. The edit costs neither strategy a Slot.
func TestAnEditToOneStrategyOfASharedGroupLosesNeitherTheInFlightSlot(t *testing.T) {
	fixture := startSharedGroupFixture(t)
	ctx := context.Background()
	inFlight := execution.EvaluationTime(fixture.base + 60)
	fixture.stallInFlight(ctx, inFlight)
	installSharedGroup(t, ctx, fixture.redisClient, map[int64][]byte{
		1001: sharedGroupStrategy(t, 1001, 1725000600, thresholdAt(2)),
		1003: sharedGroupStrategy(t, 1003, 1725000000, nil),
	})
	fixture.cutOverAt(ctx, inFlight+1)
	if ended := fixture.runUntilEnded(ctx, inFlight); ended != fullEnding {
		t.Fatalf("the in-flight Slot ended %q, want FULL_COMPLETED: it was thrown away", ended)
	}
	fixture.wantEvents(inFlight, []string{"1001", "1003"}, nil)
	// And the new Segment's first Slot, the edit carried, is evaluated.
	first := inFlight + 60
	if ended := fixture.runUntilEnded(ctx, first); ended != fullEnding {
		t.Fatalf("the new Segment's first Slot ended %q, want FULL_COMPLETED", ended)
	}
	fixture.wantEvents(first, []string{"1001", "1003"}, nil)
}

// A strategy added to a shared Query Group while a Slot is in flight costs
// the strategies already there nothing: the in-flight Slot completes for both,
// and the added one, in no Segment that Slot was frozen in, writes nothing
// for it. The new Segment's first Slot is evaluated for all three - the added
// strategy's first, judged new, is not spent warming up.
func TestAStrategyAddedToASharedGroupCostsItsStrategiesNoInFlightSlot(t *testing.T) {
	fixture := startSharedGroupFixture(t)
	ctx := context.Background()
	inFlight := execution.EvaluationTime(fixture.base + 60)
	fixture.stallInFlight(ctx, inFlight)
	installSharedGroup(t, ctx, fixture.redisClient, map[int64][]byte{
		1001: sharedGroupStrategy(t, 1001, 1725000000, nil),
		1003: sharedGroupStrategy(t, 1003, 1725000000, nil),
		1004: sharedGroupStrategy(t, 1004, 1725000600, nil),
	})
	fixture.cutOverAt(ctx, inFlight+1)
	if ended := fixture.runUntilEnded(ctx, inFlight); ended != fullEnding {
		t.Fatalf("the in-flight Slot ended %q, want FULL_COMPLETED", ended)
	}
	fixture.wantEvents(inFlight, []string{"1001", "1003"}, []string{"1004"})
	first := inFlight + 60
	if ended := fixture.runUntilEnded(ctx, first); ended != fullEnding {
		t.Fatalf("the new Segment's first Slot ended %q, want FULL_COMPLETED: the added strategy's first Slot is not a warm-up", ended)
	}
	fixture.wantEvents(first, []string{"1001", "1003", "1004"}, nil)
}

// A strategy deleted from a shared Query Group while a Slot is in flight is
// not let through: it is in no Segment after the one that Slot was frozen in,
// so its part of the Slot writes nothing. The strategy that stayed writes its
// events for that Slot as ever.
func TestAStrategyDeletedFromASharedGroupWritesNothingForItsInFlightSlot(t *testing.T) {
	fixture := startSharedGroupFixture(t)
	ctx := context.Background()
	inFlight := execution.EvaluationTime(fixture.base + 60)
	fixture.stallInFlight(ctx, inFlight)
	installSharedGroup(t, ctx, fixture.redisClient, map[int64][]byte{
		1003: sharedGroupStrategy(t, 1003, 1725000000, nil),
	})
	fixture.cutOverAt(ctx, inFlight+1)
	ended := fixture.runUntilEnded(ctx, inFlight)
	fixture.wantEvents(inFlight, []string{"1003"}, []string{"1001"})
	if want := "COMPLETED_WITH_PARTIAL_GAP/" + string(contract.ReasonPlanNotActive); ended != want {
		t.Fatalf("the in-flight Slot ended %q, want %q: the deleted strategy's part is not active", ended, want)
	}
}

// sharedGroupOf is the shared Query Group's strategies by id, unedited.
func sharedGroupOf(t *testing.T, ids ...int64) map[int64][]byte {
	strategies := make(map[int64][]byte, len(ids))
	for _, id := range ids {
		strategies[id] = sharedGroupStrategy(t, id, 1725000000, nil)
	}
	return strategies
}

// joinedAndRan adds strategy 1004 to the shared Query Group at a second
// before the Slot given, and runs that Slot: the strategy's first, judged new
// and evaluated, which leaves the satisfied marker that says it has run.
func (fixture *cutoverStallFixture) joinedAndRan(ctx context.Context, slot execution.EvaluationTime) {
	fixture.t.Helper()
	installSharedGroup(fixture.t, ctx, fixture.redisClient, sharedGroupOf(fixture.t, 1001, 1003, 1004))
	fixture.cutOverAt(ctx, slot-30)
	if ended := fixture.runUntilEnded(ctx, slot); ended != fullEnding {
		fixture.t.Fatalf("the added strategy's first Slot ended %q, want FULL_COMPLETED", ended)
	}
	fixture.wantEvents(slot, []string{"1001", "1003", "1004"}, nil)
}

// leaveAndComeBack takes strategy 1004 out of the shared Query Group at one
// second and puts it back, as it was, at the next: under the same state
// generation it comes back to the state it left (returning), and is warmed up
// whole on its first Slot back.
func (fixture *cutoverStallFixture) leaveAndComeBack(ctx context.Context, left execution.EvaluationTime) {
	fixture.t.Helper()
	installSharedGroup(fixture.t, ctx, fixture.redisClient, sharedGroupOf(fixture.t, 1001, 1003))
	fixture.cutOverAt(ctx, left)
	installSharedGroup(fixture.t, ctx, fixture.redisClient, sharedGroupOf(fixture.t, 1001, 1003, 1004))
	fixture.cutOverAt(ctx, left+1)
}

// A strategy that left a shared Query Group and came back is warmed up whole
// on its first Slot back (PLAN_REACTIVATED). Only that strategy: the two
// beside it write their events on that Slot as on any other, where the whole
// Slot used to be returned for the one strategy's warm-up.
func TestAWarmUpOfOneStrategyOfASharedGroupCostsTheOthersNothing(t *testing.T) {
	fixture := startSharedGroupFixture(t)
	ctx := context.Background()
	ran := execution.EvaluationTime(fixture.base + 60)
	fixture.joinedAndRan(ctx, ran)
	fixture.leaveAndComeBack(ctx, ran+1)
	first := ran + 60
	ended := fixture.runUntilEnded(ctx, first)
	if want := "COMPLETED_WITH_PARTIAL_GAP/" + string(contract.ReasonPlanReactivated); ended != want {
		t.Fatalf("the warm-up Slot ended %q, want %q", ended, want)
	}
	fixture.wantEvents(first, []string{"1001", "1003"}, []string{"1004"})
}

// A strategy leaves a shared Query Group (S2) and comes back (S3) while a
// Slot of the Segment it was in (S1) has not run. Run after that, the Slot
// does not let the strategy through: it was not in every Segment since, so
// an alert the absence closed while it was away is not opened again under
// the epoch it left with; the strategies beside it write their events. Back,
// its first Slot warms it up (PLAN_REACTIVATED), and still in flight when
// another edit closes S3, that Slot ends on the same word.
func TestAStrategyThatLeftASharedGroupAndCameBackIsNotLetThroughBeforeIt(t *testing.T) {
	fixture := startSharedGroupFixture(t)
	ctx := context.Background()
	ran := execution.EvaluationTime(fixture.base + 60)
	fixture.joinedAndRan(ctx, ran)
	backlog := ran + 60
	fixture.leaveAndComeBack(ctx, backlog+1)
	ended := fixture.runUntilEnded(ctx, backlog)
	if want := "COMPLETED_WITH_PARTIAL_GAP/" + string(contract.ReasonPlanNotActive); ended != want {
		t.Fatalf("the Slot before the strategy left ended %q, want %q", ended, want)
	}
	fixture.wantEvents(backlog, []string{"1001", "1003"}, []string{"1004"})

	first := backlog + 60
	fixture.stallInFlight(ctx, first)
	edited := sharedGroupOf(t, 1001, 1004)
	edited[1003] = sharedGroupStrategy(t, 1003, 1725000600, thresholdAt(2))
	installSharedGroup(t, ctx, fixture.redisClient, edited)
	fixture.cutOverAt(ctx, execution.EvaluationTime(fixture.now().Unix()+1))
	ended = fixture.runUntilEnded(ctx, first)
	if want := "COMPLETED_WITH_PARTIAL_GAP/" + string(contract.ReasonPlanReactivated); ended != want {
		t.Fatalf("the strategy's first Slot back, run after its Segment closed, ended %q, want %q", ended, want)
	}
	fixture.wantEvents(first, []string{"1001", "1003"}, []string{"1004"})
}

// backlogAcrossAnEdit leaves two Slots of the shared Query Group unrun and
// then edits strategy 1001, cut over after them: both are dated in the
// Segment the edit closed, and run after it.
func (fixture *cutoverStallFixture) backlogAcrossAnEdit(ctx context.Context) []execution.EvaluationTime {
	fixture.t.Helper()
	backlog := []execution.EvaluationTime{execution.EvaluationTime(fixture.base + 60), execution.EvaluationTime(fixture.base + 120)}
	edited := sharedGroupOf(fixture.t, 1003)
	edited[1001] = sharedGroupStrategy(fixture.t, 1001, 1725000600, thresholdAt(2))
	installSharedGroup(fixture.t, ctx, fixture.redisClient, edited)
	fixture.cutOverAt(ctx, backlog[1]+1)
	return backlog
}

// A backlog across an edit - Slots dated before the boundary, run after it,
// within the replay's reach and while the closed Segment's snapshot is kept -
// is all made up, for the edited strategy and the one beside it, instead of
// being thrown away as if both had left; the new Segment goes on after it.
func TestABacklogAcrossAnEditIsMadeUpWhileItsSnapshotIsKept(t *testing.T) {
	fixture := startSharedGroupFixture(t)
	ctx := context.Background()
	backlog := fixture.backlogAcrossAnEdit(ctx)
	for _, slot := range append(backlog, backlog[1]+60) {
		if ended := fixture.runUntilEnded(ctx, slot); ended != fullEnding {
			t.Fatalf("Slot %d ended %q, want FULL_COMPLETED; endings %v", slot, ended, fixture.slotEndings())
		}
		fixture.wantEvents(slot, []string{"1001", "1003"}, nil)
	}
}

// The same backlog after the closed Segment's snapshot has passed its
// retention - only the current activation's is renewed - cannot be read, and
// ends as a snapshot it could not read: SNAPSHOT_UNAVAILABLE, not
// PLAN_NOT_ACTIVE, which would send a reader to look for a strategy that
// left.
func TestABacklogAcrossAnEditPastItsSnapshotEndsUnavailable(t *testing.T) {
	fixture := startSharedGroupFixture(t)
	ctx := context.Background()
	backlog := fixture.backlogAcrossAnEdit(ctx)
	expireOldContent(t, fixture, fixture.initialSchedule)
	if err := fixture.repository.ConfigureObjectCache(1, 1); err != nil {
		t.Fatal(err)
	}
	ended := fixture.runUntilEnded(ctx, backlog[0])
	if !strings.HasSuffix(ended, "/"+string(contract.ReasonSnapshotUnavailable)) {
		t.Fatalf("the Slot past its snapshot ended %q, want SNAPSHOT_UNAVAILABLE; endings %v", ended, fixture.slotEndings())
	}
	fixture.wantEvents(backlog[0], nil, []string{"1001", "1003"})
}

// A warm-up Slot of one strategy of a shared Query Group is two attempts: the
// first writes the others' events and State and then the warmed-up
// strategy's marker, and returns the Slot unfinished; the second, the same
// Slot, finds the marker it wrote at its own version and commits. Retried,
// the warmed-up strategy is still skipped - judged satisfied, it would write
// its own key again at the same version and conflict on every attempt - the
// Slot keeps the first attempt's word, and the others' events are not sent
// again. Both words: a strategy back (PLAN_REACTIVATED) - to a marker it had,
// or, from the deployment's first activation and so without a marker, to the
// state it left - and an edit that carries nothing (CONFIG_DRIFT).
func TestAWarmUpSlotRetriedKeepsItsWordAndSendsNothingTwice(t *testing.T) {
	for _, test := range []struct {
		name    string
		word    string
		away    string
		staying []string
		setUp   func(fixture *cutoverStallFixture, ctx context.Context) execution.EvaluationTime
	}{
		{name: "back to its marker", word: contract.ReasonPlanReactivated, away: "1004", staying: []string{"1001", "1003"},
			setUp: func(fixture *cutoverStallFixture, ctx context.Context) execution.EvaluationTime {
				ran := execution.EvaluationTime(fixture.base + 60)
				fixture.joinedAndRan(ctx, ran)
				fixture.leaveAndComeBack(ctx, ran+1)
				return ran + 60
			}},
		{name: "back to its state, without a marker", word: contract.ReasonPlanReactivated, away: "1001", staying: []string{"1003"},
			setUp: func(fixture *cutoverStallFixture, ctx context.Context) execution.EvaluationTime {
				installSharedGroup(fixture.t, ctx, fixture.redisClient, sharedGroupOf(fixture.t, 1003))
				fixture.cutOverAt(ctx, execution.EvaluationTime(fixture.base+30))
				installSharedGroup(fixture.t, ctx, fixture.redisClient, sharedGroupOf(fixture.t, 1001, 1003))
				fixture.cutOverAt(ctx, execution.EvaluationTime(fixture.base+31))
				return execution.EvaluationTime(fixture.base + 60)
			}},
		{name: "an edit that carries nothing", word: contract.ReasonConfigDrift, away: "1001", staying: []string{"1003"},
			setUp: func(fixture *cutoverStallFixture, ctx context.Context) execution.EvaluationTime {
				installSharedGroup(fixture.t, ctx, fixture.redisClient, map[int64][]byte{
					1001: sharedGroupStrategy(fixture.t, 1001, 1725000600, renumberLevels),
					1003: sharedGroupStrategy(fixture.t, 1003, 1725000000, nil),
				})
				fixture.cutOverAt(ctx, execution.EvaluationTime(fixture.base+30))
				return execution.EvaluationTime(fixture.base + 60)
			}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := startSharedGroupFixture(t)
			ctx := context.Background()
			slot := test.setUp(fixture, ctx)
			before := len(fixture.observed())
			ended := fixture.runUntilEnded(ctx, slot)
			if want := "COMPLETED_WITH_PARTIAL_GAP/" + test.word; ended != want {
				t.Fatalf("the warm-up Slot ended %q, want %q", ended, want)
			}
			retries := 0
			for _, observation := range fixture.observed()[before:] {
				if observation.ReasonCode == observability.ReasonCode(contract.ReasonGapGuardConflict) {
					t.Fatalf("the warm-up Slot's attempts conflicted on a marker: %+v", observation)
				}
				if observation.Stage == observability.StageSlotCompleted && observation.Result == "retrying" &&
					execution.EvaluationTime(observation.Trace.EvaluationTime) == slot {
					retries++
				}
			}
			if retries == 0 {
				t.Fatal("the warm-up Slot completed in one attempt: the retry after the marker is not exercised")
			}
			fixture.wantEvents(slot, test.staying, []string{test.away})
			for _, strategy := range test.staying {
				events, ids := fixture.eventsFor(slot, strategy), map[string]bool{}
				for _, event := range events {
					if ids[event.EventID] {
						t.Fatalf("strategy %s's event %s was sent twice for the warm-up Slot", strategy, event.EventID)
					}
					ids[event.EventID] = true
				}
			}
		})
	}
}
