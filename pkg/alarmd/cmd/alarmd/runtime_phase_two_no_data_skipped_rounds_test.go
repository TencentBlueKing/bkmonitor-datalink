// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The cases in this file are the rounds in which a Plan's no-data is not
// judged, run through the production bundle on a real redis-server. Each one
// holds the round to the same three things the decomposition's user rulings
// ask of every such round (nodata-capability-decomposition section 5.9 items
// 1 and 3): the round lands on its own named outcome and on no other; it
// writes nothing to the Plan's no-data record and sends nothing about absence;
// and the threshold detection of the same Plan in the same round goes on. The
// outcome names are written out as the decomposition and the decisions spell
// them, not read off the implementation's constants.

// thresholdAbnormalIn fails the case unless the round sent exactly one
// threshold event about host A and it raised the alert: the threshold half of
// the Plan ran in a round whose no-data half did not.
func thresholdAbnormalIn(t *testing.T, round int64, events []contract.TriggerEventV1) {
	t.Helper()
	thresholdAbnormalAbout(t, round, events, "bk_target_ip", followsHostA)
}

// thresholdAbnormalAbout is thresholdAbnormalIn for the host the dimension
// names at the value.
func thresholdAbnormalAbout(t *testing.T, round int64, events []contract.TriggerEventV1, dimension, value string) {
	t.Helper()
	_, threshold := eventsAbout(events, dimension, value)
	if len(threshold) != 1 || threshold[0].EventKind != contract.TriggerEventAbnormal {
		t.Fatalf("round %d sent the threshold events %+v about %s=%s; want the one ABNORMAL its value raises: "+
			"the threshold detection must go on in a round whose no-data is skipped", round, threshold, dimension, value)
	}
}

// noNoDataEventsIn fails the case when the round sent anything about absence.
func noNoDataEventsIn(t *testing.T, round int64, events []contract.TriggerEventV1) {
	t.Helper()
	for _, event := range events {
		if noDataTagged(event) {
			t.Fatalf("round %d sent a no-data event %+v; a round that did not judge absence says nothing "+
				"about it, neither an anomaly nor a recovery", round, event)
		}
	}
}

// outcomesAre fails the case unless the round's outcomes are exactly want.
func outcomesAre(t *testing.T, round int64, got map[string]int, want map[string]int) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round %d no-data outcomes = %v, want %v", round, got, want)
	}
}

// A record a newer build wrote is neither read nor written.
//
// decision-008 section 3: a record whose schema this build does not know reads
// as unreadable, and it is kept as it is, not cleared. decision-018 section 1.2,
// for an older build meeting a newer build's record: the round that meets one
// produces no synthetic series and no mutation and does not fail; while that
// lasts the Plan's no-data detection is paused, and once a build that can read
// the record is back the absence is still counted from its first_absent. The
// newest schema this build writes is 3 (decision-018 section 1.1), so a
// header stating 4 is what a newer build leaves behind.
func TestANoDataRecordANewerBuildWroteIsNeitherReadNorWritten(t *testing.T) {
	fixture := startRoundsFixture(t, roundsOptions{hosts: true})
	ctx := context.Background()
	hostB := followsGroupKey(followsHostB)

	fixture.run(1)
	if got, want := fixture.firstAbsentOf(hostB), fixture.evaluationAt(1); got != want {
		t.Fatalf("fixture: host B's absence starts at %d after round 1, want %d", got, want)
	}
	key, record := fixture.noDataRecord()
	var header map[string]any
	if err := json.Unmarshal([]byte(record["_meta"]), &header); err != nil {
		t.Fatalf("fixture: the record header %q does not decode: %v", record["_meta"], err)
	}
	if header["version"] != float64(3) {
		t.Fatalf("fixture: the record header states version %v, want 3, the schema this build writes", header["version"])
	}
	header["version"] = 4
	newer, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.redis.HSet(ctx, key, "_meta", newer).Err(); err != nil {
		t.Fatal(err)
	}
	_, before := fixture.noDataRecord()

	fixture.value.Store(hostThresholdValue)
	mark, sent := fixture.mark(), len(fixture.written())
	fixture.run(2)
	outcomesAre(t, 2, fixture.outcomesSince(mark), map[string]int{"SKIPPED_MEMORY_UNREADABLE": 1})
	round := fixture.written()[sent:]
	noNoDataEventsIn(t, 2, round)
	thresholdAbnormalIn(t, 2, round)
	_, after := fixture.noDataRecord()
	fixture.sameRecord("a round that met a newer build's record", before, after)

	// The build that wrote it is gone and the record reads again. The round
	// judges, and host B's absence is still the one that began in round 1:
	// the record holds timestamps, so the paused round cost nothing.
	header["version"] = 3
	restored, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.redis.HSet(ctx, key, "_meta", restored).Err(); err != nil {
		t.Fatal(err)
	}
	mark = fixture.mark()
	fixture.run(3)
	outcomesAre(t, 3, fixture.outcomesSince(mark), map[string]int{"EVALUATED": 1})
	if got, want := fixture.firstAbsentOf(hostB), fixture.evaluationAt(1); got != want {
		t.Fatalf("host B's absence starts at %d after the record read again, want %d: the pause must not "+
			"restart an absence it did not judge", got, want)
	}
}

// A record that could not be read is not a record with nothing in it.
//
// decomposition section 5.9 item 1: a failure to load the Plan's memory is
// that Plan's named outcome for the round, and the threshold results and the
// progress are committed as usual; item 3: a record that cannot be read
// produces no anomaly and no recovery. The name for a round whose memory
// was not read is SKIPPED_DERIVATION_FAILED (the outcome list at the end of
// section 5.9). Two ways the read fails, both on a real redis-server: the
// read itself errors (here the key holds a value of another type), which the
// store reports as UNAVAILABLE; and the record is there and one of its group
// fields is not a value this schema writes, which the store reports as
// TERMINAL.
func TestANoDataMemoryThatCouldNotBeReadSkipsTheRound(t *testing.T) {
	for _, damage := range []struct {
		name string
		// apply damages the record at key and returns what restores it.
		apply func(t *testing.T, fixture *roundsFixture, key string) func()
	}{
		{name: "the read fails (UNAVAILABLE)", apply: func(t *testing.T, fixture *roundsFixture, key string) func() {
			ctx := context.Background()
			if err := fixture.redis.Rename(ctx, key, key+":held-aside").Err(); err != nil {
				t.Fatal(err)
			}
			if err := fixture.redis.Set(ctx, key, "not a hash", 0).Err(); err != nil {
				t.Fatal(err)
			}
			return func() {
				if err := fixture.redis.Del(ctx, key).Err(); err != nil {
					t.Fatal(err)
				}
				if err := fixture.redis.Rename(ctx, key+":held-aside", key).Err(); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{name: "a group field does not decode (TERMINAL)", apply: func(t *testing.T, fixture *roundsFixture, key string) func() {
			ctx := context.Background()
			field := "g:" + followsGroupKey("192.0.2.3")
			if err := fixture.redis.HSet(ctx, key, field, "{").Err(); err != nil {
				t.Fatal(err)
			}
			return func() {
				if err := fixture.redis.HDel(ctx, key, field).Err(); err != nil {
					t.Fatal(err)
				}
			}
		}},
	} {
		t.Run(damage.name, func(t *testing.T) {
			fixture := startRoundsFixture(t, roundsOptions{hosts: true})
			ctx := context.Background()
			hostB := followsGroupKey(followsHostB)

			fixture.run(1)
			if got, want := fixture.firstAbsentOf(hostB), fixture.evaluationAt(1); got != want {
				t.Fatalf("fixture: host B's absence starts at %d after round 1, want %d", got, want)
			}
			key, _ := fixture.noDataRecord()
			restore := damage.apply(t, fixture, key)
			stored := func() []string {
				kind, err := fixture.redis.Type(ctx, key).Result()
				if err != nil {
					t.Fatal(err)
				}
				if kind == "hash" {
					fields, err := fixture.redis.HGetAll(ctx, key).Result()
					if err != nil {
						t.Fatal(err)
					}
					encoded, _ := json.Marshal(fields)
					return []string{kind, string(encoded)}
				}
				value, err := fixture.redis.Get(ctx, key).Result()
				if err != nil {
					t.Fatal(err)
				}
				return []string{kind, value}
			}
			before := stored()

			fixture.value.Store(hostThresholdValue)
			mark, sent := fixture.mark(), len(fixture.written())
			fixture.run(2)
			outcomesAre(t, 2, fixture.outcomesSince(mark), map[string]int{"SKIPPED_DERIVATION_FAILED": 1})
			round := fixture.written()[sent:]
			noNoDataEventsIn(t, 2, round)
			thresholdAbnormalIn(t, 2, round)
			if after := stored(); !reflect.DeepEqual(before, after) {
				t.Fatalf("the round that could not read the record wrote to it:\nbefore %v\nafter  %v", before, after)
			}

			// Read again, the record still holds host B's absence from
			// round 1: the skipped round moved no clock.
			restore()
			mark = fixture.mark()
			fixture.run(3)
			outcomesAre(t, 3, fixture.outcomesSince(mark), map[string]int{"EVALUATED": 1})
			if got, want := fixture.firstAbsentOf(hostB), fixture.evaluationAt(1); got != want {
				t.Fatalf("host B's absence starts at %d once the record reads again, want %d", got, want)
			}
		})
	}
}

// A static target is judged only against a CMDB index that can answer.
//
// decomposition section 5.3 A10, and section 5.9 item 3: a dependency that
// could not be read is not an empty expected set. A static target whose hosts
// cannot be resolved lands on SKIPPED_HOSTS_UNRESOLVED: nothing is judged,
// nothing is remembered and no series is produced. The index this bundle
// builds at startup from a host cache with no host in it is the one state
// this can be shown in through the production wiring: the bundle reads its
// index on the real clock, so an index gone stale cannot be produced here, and
// the staleness rule has its own cases in the cmdbcache package. The other
// side is the same strategy on an index that holds the target's hosts, which
// judges.
func TestAStaticTargetIsJudgedOnlyAgainstACMDBIndexThatCanAnswer(t *testing.T) {
	t.Run("an index holding no host", func(t *testing.T) {
		fixture := startRoundsFixture(t, roundsOptions{hosts: false})
		fixture.value.Store(hostThresholdValue)
		mark := fixture.mark()
		fixture.run(1)
		outcomesAre(t, 1, fixture.outcomesSince(mark), map[string]int{"SKIPPED_HOSTS_UNRESOLVED": 1})
		round := fixture.written()
		noNoDataEventsIn(t, 1, round)
		thresholdAbnormalIn(t, 1, round)
		if key, fields := fixture.noDataRecord(); key != "" {
			t.Fatalf("a round that could not resolve its target stored a no-data record %s = %v", key, fields)
		}
	})
	t.Run("an index holding the target's hosts", func(t *testing.T) {
		fixture := startRoundsFixture(t, roundsOptions{hosts: true})
		fixture.value.Store(hostThresholdValue)
		mark := fixture.mark()
		fixture.run(1)
		outcomesAre(t, 1, fixture.outcomesSince(mark), map[string]int{"EVALUATED": 1})
		round := fixture.written()
		thresholdAbnormalIn(t, 1, round)
		noDataB, _ := eventsAbout(round, "bk_target_ip", followsHostB)
		if len(noDataB) != 1 || noDataB[0].EventKind != contract.TriggerEventAbnormal {
			t.Fatalf("round 1 sent host B's no-data events %+v; want the one anomaly its absence raises", noDataB)
		}
		if got, want := fixture.firstAbsentOf(followsGroupKey(followsHostB)), fixture.evaluationAt(1); got != want {
			t.Fatalf("host B's absence starts at %d, want %d", got, want)
		}
	})
}

// A Plan that keeps skipping is reported once, on its third consecutive
// skipped round, and again only after a round that judged.
//
// The rule is the user's (decomposition section 5.9 item 3, 2026-09-16): an
// occasional skipped round is allowed, a persistent stall must not pass for
// one, and a Plan skipping round after round has to be visible. The number is
// the one the progress report states for that reading (nodata-progress
// section 6, Q6): a stall is counted after three consecutive skipped rounds.
// The skips here are rounds whose query came back partial, each its own
// round on the bundle's clock; the round in the middle is complete and
// judges. Per round, the stall reports expected are written out: none for
// rounds 1 and 2, one on round 3, none on rounds 4 and 5 (the same stall is
// not counted again), none on the judged round 6, none on rounds 7 and 8 (the
// count started again), and one on round 9.
func TestAPersistentlySkippedPlanIsReportedOnItsThirdRoundAndAgainAfterItJudged(t *testing.T) {
	fixture := startRoundsFixture(t, roundsOptions{hosts: true})
	const notFull = "SKIPPED_QUERY_NOT_FULL"
	for index, step := range []struct {
		full   bool
		stalls []string
	}{
		{full: false},
		{full: false},
		{full: false, stalls: []string{notFull}},
		{full: false},
		{full: false},
		{full: true},
		{full: false},
		{full: false},
		{full: false, stalls: []string{notFull}},
	} {
		round := int64(index + 1)
		fixture.partial.Store(!step.full)
		mark := fixture.mark()
		fixture.run(round)
		want := map[string]int{notFull: 1}
		if step.full {
			want = map[string]int{"EVALUATED": 1}
		}
		outcomesAre(t, round, fixture.outcomesSince(mark), want)
		if got := fixture.stallsSince(mark); !reflect.DeepEqual(got, step.stalls) {
			t.Fatalf("round %d reported stalls %v, want %v", round, got, step.stalls)
		}
	}
}

// A round that judged and whose verdicts could not be turned into series is
// skipped by that name, and nothing of it is said or remembered.
//
// decomposition section 5.9 item 1: a failure to convert the round's output is
// the Plan's named outcome, SKIPPED_OUTPUT_FAILED, and the threshold results
// and the progress are committed as usual. Item 3 (a): the memory is written
// only for output that was said, so a round whose verdicts did not reach the
// evaluation leaves the record as it found it, and that holds for the
// verdicts that could have been built too: half a round's series would
// report some groups and say nothing about the rest.
//
// The Plan has no target, so its roster is its history. The record holds a
// group whose dimension name is not valid UTF-8, stored present: the history
// expects it, the round finds it absent, and its synthetic series cannot be
// given an identity, because a dimension identity names its dimensions in
// valid UTF-8. This build never writes such a key - the dimensions it keys
// groups by come out of a JSON query answer - so the record stands for one
// written by something else; it is the one input that reaches the conversion
// with a verdict the conversion refuses.
func TestANoDataRoundWhoseSeriesCannotBeBuiltIsSkippedAndSaysNothing(t *testing.T) {
	fixture := startRoundsFixture(t, roundsOptions{hosts: true, item: func(item map[string]any) {
		item["target"] = []any{}
	}})
	ctx := context.Background()

	mark := fixture.mark()
	fixture.run(1)
	outcomesAre(t, 1, fixture.outcomesSince(mark), map[string]int{"EVALUATED": 1})
	key, _ := fixture.noDataRecord()
	if value, held := fixture.memoryOf(followsGroupKey(followsHostA)); !held || value != "p" {
		t.Fatalf("fixture: host A is remembered as %q (held %t), want present: the history roster grows from it", value, held)
	}
	unnamed := "g:\xff=x," + contract.NoDataDimensionTag + "=true"
	if err := fixture.redis.HSet(ctx, key, unnamed, "p").Err(); err != nil {
		t.Fatal(err)
	}
	_, before := fixture.noDataRecord()

	fixture.value.Store(hostThresholdValue)
	mark, sent := fixture.mark(), len(fixture.written())
	fixture.run(2)
	outcomesAre(t, 2, fixture.outcomesSince(mark), map[string]int{"SKIPPED_OUTPUT_FAILED": 1})
	round := fixture.written()[sent:]
	noNoDataEventsIn(t, 2, round)
	thresholdAbnormalIn(t, 2, round)
	_, after := fixture.noDataRecord()
	fixture.sameRecord("a round whose series could not be built", before, after)

	// Without the group the conversion refuses, the same record and the same
	// answer are judged: the skip was that round's output, not the Plan.
	if err := fixture.redis.HDel(ctx, key, unnamed).Err(); err != nil {
		t.Fatal(err)
	}
	mark = fixture.mark()
	fixture.run(3)
	outcomesAre(t, 3, fixture.outcomesSince(mark), map[string]int{"EVALUATED": 1})
}
