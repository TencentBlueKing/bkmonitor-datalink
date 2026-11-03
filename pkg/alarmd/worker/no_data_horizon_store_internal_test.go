// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

// storeRounds runs one Plan's no-data rounds against a real store, the way a
// Slot does: load the memory, decide the round, and write the memory through
// the coordinator's own write path.
type storeRounds struct {
	t     *testing.T
	store *state.ExecutionStore
	due   execution.DuePlan
	// written is every memory write the store reported, in order.
	written []observability.NoDataMemoryWriteFacts
}

func (rounds *storeRounds) run(at int64, seen []map[string]string) (noDataRound, execution.NoDataMemorySnapshot) {
	rounds.t.Helper()
	ctx := context.Background()
	stream := noDataWiredStream(rounds.t, rounds.due, rounds.store)
	stream.header.Contract.Slot.EvaluationTime = execution.EvaluationTime(at)
	stream.request = execution.SlotExecutionRequest{Contract: stream.header.Contract, Operation: execution.OperationNormal}
	stream.coordinator.ports.Observer = observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		switch observation.Stage {
		case observability.StageNoDataMemoryWritten:
			rounds.written = append(rounds.written, *observation.NoDataMemoryWrite)
		case observability.StageNoDataMemoryRefused:
			rounds.t.Fatalf("round at %d: the store refused the memory: %+v", at, observation.NoDataMemoryRefusal)
		}
	})
	if err := stream.loadNoDataMemory(ctx); err != nil {
		rounds.t.Fatal(err)
	}
	loaded, found := stream.noData.Find(rounds.due.NoDataIdentity())
	if !found {
		rounds.t.Fatalf("round at %d: no memory was loaded for the Plan", at)
	}
	round, err := stream.noDataRoundFor(rounds.due, seen, execution.CompletenessFull)
	if err != nil {
		rounds.t.Fatalf("round at %d: %v", at, err)
	}
	if round.outcome != nodata.OutcomeEvaluated {
		rounds.t.Fatalf("round at %d: outcome %q, want the round to have judged", at, round.outcome)
	}
	if round.mutation != nil {
		before := len(rounds.written)
		if err := stream.coordinator.applyNoDataMemory(ctx, stream.request, stream.header.DuePlans,
			[]execution.PlanNoDataMutation{*round.mutation}); err != nil {
			rounds.t.Fatalf("round at %d: %v", at, err)
		}
		if len(rounds.written) != before+1 || !rounds.written[len(rounds.written)-1].Stored {
			rounds.t.Fatalf("round at %d: the memory write was not stored: %+v", at, rounds.written[before:])
		}
	}
	return round, loaded
}

// load reads the memory back as the next round would.
func (rounds *storeRounds) load(at int64) execution.NoDataMemorySnapshot {
	rounds.t.Helper()
	stream := noDataWiredStream(rounds.t, rounds.due, rounds.store)
	stream.header.Contract.Slot.EvaluationTime = execution.EvaluationTime(at)
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		rounds.t.Fatal(err)
	}
	snapshot, found := stream.noData.Find(rounds.due.NoDataIdentity())
	if !found {
		rounds.t.Fatal("no memory was loaded for the Plan")
	}
	return snapshot
}

// scriptCalls is how many scripts the server has run since its counters were
// last reset, read off its own command statistics.
func scriptCalls(t *testing.T, admin *redis.Client) int {
	t.Helper()
	info, err := admin.Info(context.Background(), "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		for _, command := range []string{"cmdstat_eval:", "cmdstat_evalsha:"} {
			if !strings.HasPrefix(line, command) {
				continue
			}
			for _, field := range strings.Split(strings.TrimPrefix(line, command), ",") {
				if value, ok := strings.CutPrefix(field, "calls="); ok {
					count, err := strconv.Atoi(value)
					if err != nil {
						t.Fatalf("command statistics line %q: %v", line, err)
					}
					calls += count
				}
			}
		}
	}
	return calls
}

// noDataHashKey is the one no-data memory record on the server.
func noDataHashKey(t *testing.T, admin *redis.Client) string {
	t.Helper()
	keys, err := admin.Keys(context.Background(), "*nodata-hash*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("no-data memory records on the server = %v, want the Plan's one", keys)
	}
	return keys[0]
}

// massExpiryGroups is how many groups expire together below: about a
// thousand, and chosen as four of the script's 256-field delete batches and
// one field more. A deletion that stopped after its first batch leaves most of
// the groups behind, and one whose batch loop stopped a field short of the end
// leaves the last batch's one field behind - which a count that fills its
// last batch with more than one field would never show.
const massExpiryGroups = 4*256 + 1

// massExpiryHost is the i-th host of the item: two hundred and fifty
// documentation addresses in each of five clouds.
func massExpiryHost(index int) map[string]string {
	return map[string]string{
		"bk_target_ip":       fmt.Sprintf("192.0.2.%d", index%250),
		"bk_target_cloud_id": strconv.Itoa(index / 250),
	}
}

// About a thousand groups of one history Plan expire on the same round. The worker
// sends no series for any of them, the store deletes all of them with one
// script, and the record that is left holds its header and nothing else (no-data
// tracking retention proposal, section 3: emptied of groups the record keeps
// its header, and section 7: a mass expiry is memory changes only, never a
// burst of closing events; decision-018 section 1.3: the deletion is one script
// whatever the count).
//
// Run against a real redis-server, on each major the deployments run, so the
// script's batching is what the server actually executes. The count of scripts
// is read off the server's own command statistics around the expiring write
// alone. The round before the horizon is run too: it still reports every group,
// so the round that reaches the horizon is the one that changes the answer.
// The round after it is run as well: the emptied history roster neither
// reports the groups again nor turns into a whole-item absence.
func TestAThousandGroupsExpiringTogetherAreDeletedByOneScriptAndSendNothing(t *testing.T) {
	address, admin := startLifetimeRedis(t)
	store := openLifetimeStore(t, address)
	// Which major ran the script, for the record of the run: the batching is
	// executed by the server, and the deployments run more than one.
	if info, err := admin.Info(context.Background(), "server").Result(); err == nil {
		for _, line := range strings.Split(info, "\n") {
			if version, ok := strings.CutPrefix(strings.TrimSpace(line), "redis_version:"); ok {
				t.Logf("redis-server %s", version)
			}
		}
	}
	const horizon = int64(600)
	due := noDataWiredPlan(t)
	due.CompiledPlan = noDataPreflightPlan(t, "7", &contract.NoDataConfigV1{
		Continuous: 1, Level: 2, AggDimension: []string{"bk_target_ip", "bk_target_cloud_id"},
		TrackingHorizonSeconds: horizon,
	})
	rounds := &storeRounds{t: t, store: store, due: due}
	start := int64(noDataPreflightContract(t, []execution.DuePlan{due}).Slot.EvaluationTime)

	seen := make([]map[string]string, massExpiryGroups)
	for index := range seen {
		seen[index] = massExpiryHost(index)
	}
	// Every group reports, then none does.
	reported, _ := rounds.run(start, seen)
	if points := pointsByHost(t, noDataPointsOf(t, reported.series)); len(points) != massExpiryGroups+1 {
		t.Fatalf("the reporting round produced %d points, want every group and the whole item", len(points))
	}
	absentFrom := start + 60
	absent, _ := rounds.run(absentFrom, nil)
	if points := pointsByHost(t, noDataPointsOf(t, absent.series)); len(points) != massExpiryGroups {
		t.Fatalf("the first absent round produced %d points, want one per group", len(points))
	}
	key := noDataHashKey(t, admin)
	if fields := admin.HLen(context.Background(), key).Val(); fields != massExpiryGroups+1 {
		t.Fatalf("the record holds %d fields before the expiry, want every group and the header", fields)
	}

	// One round short of the horizon: every group is still reported.
	short, _ := rounds.run(absentFrom+horizon-60, nil)
	for host, value := range pointsByHost(t, noDataPointsOf(t, short.series)) {
		if value != strconv.Itoa(nodata.AbsentValue) {
			t.Fatalf("one round short of the horizon %s gave %s, want it still reported absent", host, value)
		}
	}
	if len(short.series) != massExpiryGroups || short.facts.Expired != 0 {
		t.Fatalf("one round short of the horizon: %d series, %d expired; want every group reported and none expired",
			len(short.series), short.facts.Expired)
	}

	// The round the horizon is reached, with the server's counters reset
	// just before it so they hold this round's write and nothing else.
	expiresAt := absentFrom + horizon
	if err := admin.ConfigResetStat(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	expired, _ := rounds.run(expiresAt, nil)
	if calls := scriptCalls(t, admin); calls != 1 {
		t.Fatalf("the expiring write ran %d scripts, want one", calls)
	}
	if len(expired.series) != 0 {
		t.Fatalf("the round the horizon was reached produced %d series, want none: a stopped absence "+
			"sends neither an anomaly nor a recovery", len(expired.series))
	}
	if expired.facts.Expired != massExpiryGroups || expired.facts.Absent != 0 {
		t.Fatalf("facts = %+v, want every group counted as expired and none reported", expired.facts)
	}
	if expired.mutation == nil || len(expired.mutation.Del) != massExpiryGroups {
		t.Fatalf("the expiring statement deletes %d groups, want all %d", len(expired.mutation.Del), massExpiryGroups)
	}
	fields, err := admin.HKeys(context.Background(), key).Result()
	if err != nil {
		t.Fatal(err)
	}
	// "_meta" is the header field of the per-Plan no-data hash
	// (decision-008; state/execution_v2_no_data_hash.go names it).
	if len(fields) != 1 || fields[0] != "_meta" {
		t.Fatalf("after the expiry the record holds %d fields (first %v), want the header alone",
			len(fields), fields[:min(len(fields), 3)])
	}
	stored := rounds.load(expiresAt + 60)
	if stored.Status != execution.NoDataMemoryFound || len(stored.Groups) != 0 || stored.TrackingExhaustedAt != expiresAt {
		t.Fatalf("the record reads back as %s with %d groups and exhausted at %d, want a found record with "+
			"no groups that says the horizon emptied it at %d",
			stored.Status, len(stored.Groups), stored.TrackingExhaustedAt, expiresAt)
	}

	// The round after: nothing comes back, and the empty roster is not read
	// as an item that never had groups.
	after, _ := rounds.run(expiresAt+60, nil)
	if len(after.series) != 0 || after.facts.Absent != 0 || after.facts.Expired != 0 {
		t.Fatalf("the round after the expiry produced %d series with facts %+v, want nothing at all",
			len(after.series), after.facts)
	}
	if fields := admin.HLen(context.Background(), key).Val(); fields != 1 {
		t.Fatalf("the round after the expiry left %d fields, want the header alone", fields)
	}
}

// An absence the horizon stopped is still an absence to a supplement, read
// out of the record the worker's own rounds wrote: the whole item goes without
// data until its tracking stops, and a series of the item that arrives late for
// a Slot inside that absence is not taken, for the round the absence started
// in, the round it stopped in, and a round after it. A Slot before the absence
// began has nothing recorded at it. See
// TestASupplementReadsAnAbsenceTheHorizonStoppedAsRecorded for the rule.
func TestASupplementLeavesAStoppedNoDataAbsenceStandingInTheStore(t *testing.T) {
	address, _ := startLifetimeRedis(t)
	store := openLifetimeStore(t, address)
	const horizon = int64(120)
	due := noDataWiredPlan(t)
	due.CompiledPlan = noDataPreflightPlan(t, "7", &contract.NoDataConfigV1{
		Continuous: 1, Level: 2, AggDimension: []string{}, TrackingHorizonSeconds: horizon,
	})
	rounds := &storeRounds{t: t, store: store, due: due}
	start := int64(noDataPreflightContract(t, []execution.DuePlan{due}).Slot.EvaluationTime)

	whole := nodata.WholeItemGroup().Key()
	for index, want := range []int{1, 1, 0, 0} {
		at := start + int64(index)*60
		round, _ := rounds.run(at, nil)
		if len(round.series) != want {
			t.Fatalf("round %d produced %d series, want %d", index, len(round.series), want)
		}
	}
	stopAt := start + horizon
	snapshot := rounds.load(start + 4*60)
	if len(snapshot.Groups) != 1 || snapshot.Groups[0].GroupKey != whole ||
		snapshot.Groups[0].FirstAbsent != start || snapshot.Groups[0].SuppressedAt != stopAt {
		t.Fatalf("the record holds %+v, want the whole item absent from %d and stopped at %d",
			snapshot.Groups, start, stopAt)
	}

	for _, slot := range []struct {
		name string
		at   int64
		want noDataStanding
	}{
		{"the Slot the absence began", start, noDataRecorded},
		{"the Slot tracking stopped", stopAt, noDataRecorded},
		{"a Slot after the stop", stopAt + 60, noDataRecorded},
		{"a Slot before the absence began", start - 60, noDataNone},
	} {
		stream := noDataWiredStream(t, due, store)
		stream.header.Contract.Slot.EvaluationTime = execution.EvaluationTime(slot.at)
		stream.request = execution.SlotExecutionRequest{Contract: stream.header.Contract, Operation: execution.OperationSupplement}
		late := supplementSeries(t, due, slot.at, map[string]string{"bk_target_ip": "192.0.2.33", "bk_target_cloud_id": "0"})
		stream.supplement = newSupplementRun(execution.SupplementScope{Series: []execution.SeriesIdentityDigest{late.identity}})
		if err := stream.loadNoDataMemory(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := stream.noDataStandingAt(late); got != slot.want {
			t.Fatalf("%s: standing %d, want %d", slot.name, got, slot.want)
		}
		if slot.want != noDataRecorded {
			continue
		}
		if stream.supplementTakes(late) {
			t.Fatalf("%s: a late series was taken inside an absence the horizon stopped", slot.name)
		}
		if want := (execution.SupplementFacts{Candidates: 1, NoDataFact: 1}); stream.supplement.facts != want {
			t.Fatalf("%s: facts %+v, want %+v", slot.name, stream.supplement.facts, want)
		}
	}
}
