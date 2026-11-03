// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// lifetimeSlack is how far a remaining life read from the server may sit from
// the one the test expects. The server counts down in real time while the
// test runs, which is milliseconds; a second-scale slack still tells a key
// renewed to a day from one left at ten hours.
const lifetimeSlack = 10 * time.Second

// remaining is the fixture Plan's per-group record's remaining life, as PTTL
// answers. Zero or less means the key is gone or has no expiry.
func (server *noDataServer) remaining(t *testing.T, key string) time.Duration {
	t.Helper()
	left, err := server.client.PTTL(context.Background(), key).Result()
	if err != nil {
		t.Fatal(err)
	}
	return left
}

// age does to one key what elapsed time does: it takes elapsed off its
// remaining life, and removes it if nothing is left. Redis keeps its own
// clock, which a test cannot move forward, so the rounds between two Slots are
// taken off the key instead.
func (server *noDataServer) age(t *testing.T, key string, elapsed time.Duration) {
	t.Helper()
	ctx := context.Background()
	left := server.remaining(t, key)
	if left <= 0 {
		t.Fatalf("aging %s by %s: it has no remaining life (%s)", key, elapsed, left)
	}
	if left <= elapsed {
		server.client.Del(ctx, key)
		return
	}
	if err := server.client.PExpire(ctx, key, left-elapsed).Err(); err != nil {
		t.Fatal(err)
	}
}

// within reports whether got is want, give or take lifetimeSlack.
func within(got, want time.Duration) bool {
	return got > want-lifetimeSlack && got <= want+time.Second
}

// edgeSlack is the slack for lifetimes whose edges are a second apart. The
// runtime arms take the one-round floor at 119, 120 and 121 seconds, and a
// floor that went missing moves the first of them by exactly one second, so a
// reading has to tell two lifetimes a second apart; the milliseconds between a
// write and its read are well inside this.
const edgeSlack = 500 * time.Millisecond

// withinEdge reports whether got is want, give or take edgeSlack. A remaining
// life never reads above what was set.
func withinEdge(got, want time.Duration) bool {
	return got > want-edgeSlack && got <= want
}

// storeNoDataFor sends one mutation carrying the Plan's retention, which is
// what decides the lifetime the write gives the key.
func storeNoDataFor(
	t *testing.T, store *ExecutionStore, retention []execution.StateRetentionRequirement,
	mutation execution.PlanNoDataMutation,
) execution.NoDataApplyItemResult {
	t.Helper()
	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
		Retention: planRetention(retention), Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
	})
	if err != nil {
		t.Fatal(err)
	}
	return applied.Items[0]
}

// loadNoDataFor loads the fixture Plan's memory the way a Slot does, with the
// Plan's retention, and returns what it read and what its renewal did.
func loadNoDataFor(
	t *testing.T, store *ExecutionStore, retention []execution.StateRetentionRequirement,
) (execution.NoDataMemorySnapshot, []execution.NoDataMemoryRenewal) {
	t.Helper()
	item := noDataLoadItemV2()
	item.Retention = retention
	loaded, err := store.LoadNoData(context.Background(), execution.NoDataLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataLoadItem{item},
	})
	if err != nil {
		t.Fatalf("a load returned an error rather than a snapshot: %v", err)
	}
	return loaded.Items[0], loaded.Renewals
}

// renewalModel is the doc's account of what a load does to the key's life,
// stated without the code that does it: the load asks the server only when the
// process has not asked within a quarter of the lifetime, and the server
// extends the key only when less than half of the lifetime is left.
//
// Sources: decision-008 section 4.5 (the load renews the per-group record
// through the same renewal as the single-value record); decision-012 section
// 2 (renew only below half the lifetime, the generation keys' existing ttl/2)
// and section 2.1.1 (a) (the ask gate's interval, half of that threshold).
type renewalModel struct {
	lifetime   time.Duration
	lastAsked  time.Duration
	everAsked  bool
	remaining  time.Duration
	clock      time.Duration
	askedNow   bool
	renewedNow bool
	renewals   int
}

func (model *renewalModel) round(elapsed time.Duration) {
	model.clock += elapsed
	model.remaining -= elapsed
	model.askedNow = !model.everAsked || model.clock-model.lastAsked >= model.lifetime/4
	model.renewedNow = false
	if model.askedNow {
		model.everAsked, model.lastAsked = true, model.clock
		if model.remaining < model.lifetime/2 {
			model.remaining, model.renewedNow = model.lifetime, true
			model.renewals++
		}
	}
}

// A steady Plan's per-group record keeps its lifetime across rounds on a real
// server, though nothing writes it after the first round.
//
// decision-008 section 4.5 is the failure this exists for: the single-value
// record was rewritten whenever anything moved and the write carried the
// lifetime, while a steady per-group record writes nothing at all, so without
// a renewal on the load it expires under a perfectly healthy Plan. Section 7
// item 6 is the reading: take the key's PTTL N rounds apart, and it must be
// moving forward rather than counting down to zero.
//
// The Plan runs every minute on five points, so the lifetime is the one-day
// floor (GenerationScopedFloor; the runtime lifetime of five minutes and the
// margin is shorter). Rounds here are seven hours apart -- more than a quarter
// of the day, so every round asks -- and six of them span 42 hours: without
// the load's renewal the key is gone by the fourth.
func TestASteadyPerGroupRecordKeepsItsLifetimeAcrossRoundsOnARealServer(t *testing.T) {
	server := startNoDataServer(t)
	store := server.store(t)
	clock := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	store.renewals.now = func() time.Time { return clock }
	retention := retentionEvery(5, time.Minute)
	key := noDataHashKey(t)
	const day = 24 * time.Hour

	if got := storeNoDataFor(t, store, retention, firstNoDataWrite(t, 0, absentGroup("steady"))); got.Status != execution.NoDataApplied {
		t.Fatalf("the Plan's one write = %+v", got)
	}
	if left := server.remaining(t, key); !within(left, day) {
		t.Fatalf("the write gave the record %s, want a day", left)
	}

	model := renewalModel{lifetime: day, remaining: day}
	const step = 7 * time.Hour
	for round := 1; round <= 6; round++ {
		server.age(t, key, step)
		clock = clock.Add(step)
		model.round(step)
		snapshot, renewals := loadNoDataFor(t, store, retention)
		if snapshot.Status != execution.NoDataMemoryFound || len(snapshot.Groups) != 1 ||
			snapshot.Groups[0] != absentGroup("steady") {
			t.Fatalf("round %d read %+v, want the one remembered group: the steady record expired under the Plan",
				round, snapshot)
		}
		if left := server.remaining(t, key); !within(left, model.remaining) {
			t.Fatalf("round %d left the record %s, want %s (asked %v, renewed %v)",
				round, left, model.remaining, model.askedNow, model.renewedNow)
		}
		if !model.askedNow {
			t.Fatalf("setup: round %d did not ask; seven hours is past a quarter of a day", round)
		}
		if len(renewals) != 1 {
			t.Fatalf("round %d reported %d renewals, want the one that reached the server", round, len(renewals))
		}
		if renewal := renewals[0]; renewal.Renewed != model.renewedNow || renewal.TTLSeconds != int64(day/time.Second) ||
			renewal.ReasonCode != "" {
			t.Fatalf("round %d renewal = %+v, want renewed=%v toward a day with no reason", round, renewal, model.renewedNow)
		}
	}
}

// A no-data hash is written with the lifetime its load renews it to, and that
// lifetime carries it past its next round, on a real server, for Plans that
// run every minute, every hour and every sixty hours.
//
// The lifetime is the doc's, worked out here by hand rather than taken from
// GenerationScopedTTL: T = max(StateTTL(retention), one day), the ruling that
// gave generation-scoped keys a lifetime renewed on the load, which decision-008
// section 1 applies to the per-group record. StateTTL is points times interval
// plus the restart margin, moved to the next whole number of steps plus half a
// step (decision-012 section 2.3), within the store's minimum and maximum. The
// margin here is a minute and the maximum thirty days.
//
//   - every minute, five points: 6m -> 6m30s, under a day, so a day.
//   - every hour, thirty points: 30h1m -> 30h30m.
//   - every sixty hours, ten points: 600h1m -> 630h, under the 720h maximum.
//   - every sixty hours, fourteen points, the deployed shape: 840h1m is past
//     the maximum, and no doc gives a figure for it. What the doc does require
//     is the property this exists for (decision-008 section 4.5): the key
//     outlives the interval to the next round, sixty hours and the margin, and
//     no key outlives the maximum.
//
// Each arm then runs rounds at its own interval with nothing written, ages the
// key between them, and asserts every round still reads the memory. The
// sixty-hour arm runs twelve rounds, 720 hours, past its own lifetime, so the
// load's renewal has to happen within the run. Every arm ends by letting the
// key fall below half its life and checking the load renews it to exactly
// what the write gave.
func TestANoDataHashOutlivesItsNextRoundOnARealServer(t *testing.T) {
	const margin = time.Minute
	for _, arm := range []struct {
		name     string
		interval time.Duration
		points   uint32
		// want is the doc's lifetime, zero where the doc gives none.
		want   time.Duration
		rounds int
	}{
		{name: "every minute", interval: time.Minute, points: 5, want: 24 * time.Hour, rounds: 3},
		{name: "every hour", interval: time.Hour, points: 30, want: 30*time.Hour + 30*time.Minute, rounds: 3},
		{name: "every sixty hours", interval: 60 * time.Hour, points: 10, want: 630 * time.Hour, rounds: 12},
		{name: "every sixty hours, past the maximum", interval: 60 * time.Hour, points: 14, rounds: 3},
	} {
		t.Run(arm.name, func(t *testing.T) {
			server := startNoDataServer(t)
			store := server.store(t)
			clock := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			store.renewals.now = func() time.Time { return clock }
			retention := retentionEvery(arm.points, arm.interval)
			key := noDataHashKey(t)

			if got := storeNoDataFor(t, store, retention, firstNoDataWrite(t, 0, absentGroup("a"))); got.Status != execution.NoDataApplied {
				t.Fatalf("write = %+v", got)
			}
			written := server.remaining(t, key)
			nextRound := arm.interval + margin
			if arm.want != 0 && !within(written, arm.want) {
				t.Fatalf("the write gave the record %s, want %s", written, arm.want)
			}
			if written < nextRound || written > 30*24*time.Hour {
				t.Fatalf("the write gave the record %s; it must outlive the next round at %s and not the 720h maximum",
					written, nextRound)
			}
			lifetime := arm.want
			if lifetime == 0 {
				lifetime = written.Round(time.Hour)
			}

			model := renewalModel{lifetime: lifetime, remaining: lifetime}
			for round := 1; round <= arm.rounds; round++ {
				server.age(t, key, arm.interval)
				clock = clock.Add(arm.interval)
				model.round(arm.interval)
				snapshot, _ := loadNoDataFor(t, store, retention)
				if snapshot.Status != execution.NoDataMemoryFound || len(snapshot.Groups) != 1 {
					t.Fatalf("round %d read %+v, want the memory still there", round, snapshot)
				}
				left := server.remaining(t, key)
				if !within(left, model.remaining) {
					t.Fatalf("round %d left the record %s, want %s (asked %v, renewed %v)",
						round, left, model.remaining, model.askedNow, model.renewedNow)
				}
				if left < nextRound {
					t.Fatalf("round %d left the record %s, which does not reach the next round at %s",
						round, left, nextRound)
				}
			}
			if time.Duration(arm.rounds)*arm.interval > lifetime && model.renewals == 0 {
				t.Fatal("setup: the rounds outlast the lifetime without a renewal; the arm is not exercising it")
			}

			// Below half its life, the next load that asks renews it to what the
			// write gave it: the write never gives less than the load renews to,
			// and the load never takes back what the write gave.
			if err := server.client.PExpire(context.Background(), key, lifetime/4).Err(); err != nil {
				t.Fatal(err)
			}
			clock = clock.Add(lifetime / 2)
			snapshot, renewals := loadNoDataFor(t, store, retention)
			if snapshot.Status != execution.NoDataMemoryFound {
				t.Fatalf("the renewing load read %+v", snapshot)
			}
			if len(renewals) != 1 || !renewals[0].Renewed {
				t.Fatalf("renewals = %+v, want the key renewed below half its life", renewals)
			}
			if left := server.remaining(t, key); !within(left, written) {
				t.Fatalf("the load renewed the record to %s, the write gave it %s; the two must agree", left, written)
			}
		})
	}
}

// A series' runtime state is written, and renewed while frozen, for the
// retention's own lifetime capped at the horizon H, and never for less than
// one round, on a real server.
//
// The rule came with the platform horizon (the no-data tracking proposal
// section 1 sets H, one day by default), when H was made to bound the state a
// series leaves behind once it stops appearing: a key lives min(R, max(H, F)),
// where R is the retention's own lifetime and F is what a series that keeps
// reporting needs to reach its next write, one interval plus its lateness plus
// the restart margin. The proposal's own section 4 still has runtime state
// expiring on its original lifetime; this rule superseded it. Here the retention is
// five one-minute points with a minute of margin, so R is 6m moved to the next
// half step, 6m30s (decision-012 section 2.3), and F is two minutes. Both
// edges are taken on either side: 119/120/121 seconds against the floor, and
// 389/390/391 against the retention.
func TestARuntimeKeyLivesTheRetentionCappedAtTheHorizonOnARealServer(t *testing.T) {
	const retentionOwn, floor = 390 * time.Second, 120 * time.Second
	server := startNoDataServer(t)
	ctx := context.Background()
	for _, arm := range []struct {
		horizon int64
		want    time.Duration
	}{
		{horizon: 0, want: retentionOwn},
		{horizon: 119, want: floor},
		{horizon: 120, want: floor},
		{horizon: 121, want: 121 * time.Second},
		{horizon: 389, want: 389 * time.Second},
		{horizon: 390, want: retentionOwn},
		{horizon: 391, want: retentionOwn},
		{horizon: 86400, want: retentionOwn},
	} {
		t.Run(fmt.Sprintf("H=%d", arm.horizon), func(t *testing.T) {
			if err := server.client.FlushAll(ctx).Err(); err != nil {
				t.Fatal(err)
			}
			store := server.store(t, func(options *ExecutionStoreOptions) { options.MaxTTL = time.Hour })
			mutation, err := execution.BuildStateMutation(execution.StateMutation{
				Identity: stateIdentityV2(), ApplyVersion: applyVersion(),
				AffectedRecords: []execution.RecordAnchor{derivedAnchor(t, stateIdentityV2(), 60)},
				Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat",
					HistoryCompleteness: execution.HistoryFull, WarmupRequirementRef: "warm", LastProcessedEventTime: 60}},
				Points: []execution.StateHistoryPoint{derivedPoint(t, stateIdentityV2(), 60, "detect", execution.LevelFactNormal)},
			})
			if err != nil {
				t.Fatal(err)
			}
			applied, err := store.ApplyRuntime(ctx, execution.StateApplyRequest{
				Contract: frozenRef(), Retention: testRetention(), Items: []execution.StateMutation{mutation},
				HorizonSeconds: arm.horizon,
			})
			if err != nil || applied.Items[0].Status != execution.StateApplied {
				t.Fatalf("ApplyRuntime() = (%+v, %v)", applied, err)
			}
			keys, err := server.client.Keys(ctx, "*").Result()
			if err != nil || len(keys) == 0 {
				t.Fatalf("the write left keys %v (%v)", keys, err)
			}
			for _, key := range keys {
				if left := server.remaining(t, key); !withinEdge(left, arm.want) {
					t.Fatalf("the write gave %s %s, want %s", key, left, arm.want)
				}
			}

			// Frozen: no write, and the key is running out. The renewal sets
			// the same capped lifetime the write did.
			key, err := RuntimeStateKeyV3("alarmd", stateIdentityV2())
			if err != nil {
				t.Fatal(err)
			}
			if err := server.client.PExpire(ctx, key, 10*time.Second).Err(); err != nil {
				t.Fatal(err)
			}
			request := frozenRequest(writtenAt().Add(arm.want))
			request.HorizonSeconds = arm.horizon
			renewed, err := store.RenewFrozenRuntime(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if outcome := renewed.Items[0].Outcome; outcome != execution.FrozenRenewalRenewed {
				t.Fatalf("frozen renewal = %+v, want the running-out key renewed", renewed.Items[0])
			}
			if left := server.remaining(t, key); !withinEdge(left, arm.want) {
				t.Fatalf("the renewal gave %s %s, want %s", key, left, arm.want)
			}
		})
	}
}

// copyRecord puts the fixture Plan's per-group record, as one server holds it,
// on another server with the same remaining life.
//
// It is how a record reaches a server whose scripting is disabled: the store
// writes through a script, so the record is written where scripting works and
// carried across field by field.
func copyRecord(t *testing.T, from, to *noDataServer) {
	t.Helper()
	ctx := context.Background()
	key := noDataHashKey(t)
	fields := from.record(t)
	if len(fields) == 0 {
		t.Fatal("copyRecord: the source holds no record")
	}
	values := make([]interface{}, 0, 2*len(fields))
	for name, value := range fields {
		values = append(values, name, value)
	}
	if err := to.client.HSet(ctx, key, values...).Err(); err != nil {
		t.Fatal(err)
	}
	if err := to.client.PExpire(ctx, key, from.remaining(t, key)).Err(); err != nil {
		t.Fatal(err)
	}
}

// A renewal the server refuses leaves the load FOUND, reports the refusal on
// the load's renewal list, and returns no error, on a real server.
//
// decision-008 section 4.5: a failed renewal is not the Plan's failure -- the
// record was read, and the read is what the caller asked for -- and it has to
// say so itself, because the write family is correctly silent on a steady
// Plan. The server here has scripting renamed away, so the header read works
// and the renewal script is refused: the record is read, and its life is left
// exactly where it was.
func TestARenewalTheServerRefusesLeavesTheMemoryFoundOnARealServer(t *testing.T) {
	writer := startNoDataServer(t)
	retention := retentionEvery(5, time.Minute)
	if got := storeNoDataFor(t, writer.store(t), retention, firstNoDataWrite(t, 0, absentGroup("a"))); got.Status != execution.NoDataApplied {
		t.Fatalf("write = %+v", got)
	}
	server := startNoDataServer(t, "--rename-command", "EVAL", "", "--rename-command", "EVALSHA", "")
	copyRecord(t, writer, server)
	key := noDataHashKey(t)
	// Below half its life, so the renewal is one the load has to make.
	if err := server.client.PExpire(context.Background(), key, 6*time.Hour).Err(); err != nil {
		t.Fatal(err)
	}

	snapshot, renewals := loadNoDataFor(t, server.store(t), retention)
	if snapshot.Status != execution.NoDataMemoryFound || snapshot.MarkerRevision != 1 ||
		len(snapshot.Groups) != 1 || snapshot.Groups[0] != absentGroup("a") {
		t.Fatalf("load = %+v, want the record read: the failed renewal is not the read's failure", snapshot)
	}
	if len(renewals) != 1 {
		t.Fatalf("renewals = %+v, want the refused one reported", renewals)
	}
	want := execution.NoDataMemoryRenewal{
		Identity: noDataIdentityV2(), Renewed: false, TTLSeconds: int64(24 * time.Hour / time.Second),
		ReasonCode: execution.ReasonCode(contract.ReasonRedisUnavailable),
	}
	if renewals[0] != want {
		t.Fatalf("renewal = %+v, want %+v", renewals[0], want)
	}
	if left := server.remaining(t, key); !within(left, 6*time.Hour) {
		t.Fatalf("the record has %s left, want the six hours it had: nothing renewed it", left)
	}
}
