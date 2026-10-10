// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// memoryRenewed is the observation the worker emits for every renewal of a
// Plan's absence-memory key that reached the store, as
// worker.observeNoDataRenewals builds it: success with the store's answer
// (renewed or "enough life left"), or degraded with the reason the renewal
// failed. The Plan is on the trace, the object on the context.
func memoryRenewed(ctx context.Context, tracker *Tracker, strategy string, renewed bool, ttl int64, reason string) {
	result, code := observability.Result(observability.ResultSuccess), observability.ReasonCode(observability.ReasonNone)
	if reason != "" {
		result, code = observability.ResultDegraded, observability.ReasonCode(reason)
	}
	tracker.Observe(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageNoDataMemoryRenewed,
		Direction: observability.DirectionInternal, Result: result, ReasonCode: code,
		Trace:               observability.TraceFields{StrategyID: strategy, BusinessID: "2"},
		NoDataMemoryRenewal: &observability.NoDataMemoryRenewalFacts{Renewed: renewed, TTLSeconds: ttl},
	})
}

// memoryRead is the observation the worker emits once per Plan per round
// saying which stored shape its memory came from.
func memoryRead(ctx context.Context, tracker *Tracker, strategy, representation string) {
	tracker.Observe(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageNoDataMemoryRead,
		Direction: observability.DirectionInternal, Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone,
		Trace:            observability.TraceFields{StrategyID: strategy, BusinessID: "2"},
		NoDataMemoryRead: &observability.NoDataMemoryReadFacts{Representation: representation},
	})
}

func fullRound(ctx context.Context, tracker *Tracker, slot int64) {
	tracker.Observe(ctx, observability.Observation{ProgressCompletionKind: "FULL_COMPLETED",
		Trace: observability.TraceFields{EvaluationTime: slot}})
}

// A renewal the store will not do is listed like a refused write -- on the
// same line, under the store's reason -- from the first refusal, counted per
// attempt, and it ends when a renewal reaches the store again, whatever the
// store then answers: "enough life left" is the ordinary answer and it is
// the positive fact. The recovery goes on the ledger under the fold the row
// was on.
func TestARefusedRenewalIsListedUntilOneReachesTheStore(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-renew"})
	for round := 0; round < 2; round++ {
		fullRound(ctx, tracker, int64(100+60*round))
		memoryRead(ctx, tracker, "s-1", "PER_GROUP")
		memoryRenewed(ctx, tracker, "s-1", false, 43200, "REDIS_UNAVAILABLE")
		at.at = at.at.Add(time.Minute)
	}
	rows := tracker.NoDataMemory()
	if len(rows) != 1 {
		t.Fatalf("no-data memory rows = %+v, want the one object", rows)
	}
	row := rows[0]
	memory := row.NoDataMemory
	if memory == nil || memory.Kind != NoDataMemoryRefusalRenewal || memory.Reason != "REDIS_UNAVAILABLE" || memory.Refusals != 2 ||
		!memory.FirstAt.Equal(now) || !memory.LastAt.Equal(now.Add(time.Minute)) || memory.Plan.StrategyID != "s-1" {
		t.Fatalf("memory = %+v, want a RENEWAL refusal under the store's reason, counted twice", memory)
	}
	if row.ReasonCode != "REDIS_UNAVAILABLE" || row.Consecutive != 2 {
		t.Fatalf("row = %+v, want the store's reason as the row's code and the attempts as its count", row)
	}
	Attribute(rows, at.at)
	if rows[0].Finding.Check != CheckNoDataMemoryRefused || rows[0].Finding.Group != "REDIS_UNAVAILABLE" {
		t.Fatalf("finding = %+v, want the memory line folded on the store's reason", rows[0].Finding)
	}
	// The round is fine; what the refusal did is to the memory's upkeep, and
	// the store is asked again every round: neither an unconfirmed result nor
	// a round being retried.
	if rows[0].Blocked == nil || rows[0].Blocked.Effect != EffectMemoryLost || rows[0].Blocked.Dependency != DependencyRedis {
		t.Fatalf("blocked = %+v, want the memory-lost effect against the store", rows[0].Blocked)
	}
	// And on the line, a refusal a minute ago is the store still refusing:
	// the fold reads BLOCKED, not recovering and not silent.
	view := View{NoDataMemory: rows}
	for _, report := range ReportChecks(nil, nil, &view, at.at) {
		if report.Code != CheckNoDataMemoryRefused {
			continue
		}
		if len(report.Groups) != 1 || report.Groups[0].Key != "REDIS_UNAVAILABLE" || report.Groups[0].Recovery != RecoveryBlocked {
			t.Fatalf("groups = %+v, want the one fold on the store's reason, blocked", report.Groups)
		}
	}
	// The upkeep beside it says what the last read found and that a renewal
	// reached the store, refused: the attempt is a fact even when it failed.
	if upkeep := row.NoDataMemoryUpkeep; upkeep == nil || upkeep.Representation != "PER_GROUP" || upkeep.LastAttemptAt == nil ||
		upkeep.LastRenewedAt != nil || upkeep.Plans != 1 {
		t.Fatalf("upkeep = %+v, want the read's shape, an attempt, and no renewal", row.NoDataMemoryUpkeep)
	}
	// The store answers again -- with "enough life left", which renews
	// nothing and is not a failure -- and the refusal is over.
	memoryRenewed(ctx, tracker, "s-1", false, 43200, "")
	if rows := tracker.NoDataMemory(); len(rows) != 0 {
		t.Fatalf("rows after a renewal reached the store = %+v, want none", rows)
	}
	recovered := tracker.Recovered()
	if len(recovered) != 1 || recovered[0].Check != CheckNoDataMemoryRefused || recovered[0].Key != "REDIS_UNAVAILABLE" || recovered[0].Objects != 1 {
		t.Fatalf("recovered = %+v, want the object on the ledger under the memory line and the store's reason", recovered)
	}
}

// A refused write is the loss that stands whatever happens to the key's
// lifetime: it outranks a refused renewal on the same Plan, a renewal going
// through does not end it, and a stored write ends both -- the write sets
// the lifetime too.
func TestAWriteRefusalOutranksARenewalRefusal(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-both"})
	fullRound(ctx, tracker, 100)
	memoryRenewed(ctx, tracker, "s-1", false, 43200, "BACKEND_CAPABILITY_MISSING")
	memoryRefused(ctx, tracker, 120400)
	rows := tracker.NoDataMemory()
	if len(rows) != 1 || rows[0].NoDataMemory.Kind != NoDataMemoryRefusalWrite || rows[0].NoDataMemory.Reason != "STATE_BUDGET_EXCEEDED" || rows[0].NoDataMemory.Refusals != 1 {
		t.Fatalf("rows = %+v, want the write refusal listed over the renewal one", rows)
	}
	memoryRenewed(ctx, tracker, "s-1", true, 43200, "")
	if rows := tracker.NoDataMemory(); len(rows) != 1 || rows[0].NoDataMemory.Kind != NoDataMemoryRefusalWrite {
		t.Fatalf("rows after a renewal went through = %+v, want the write refusal still listed", rows)
	}
	// A renewal refusal on a Plan already listed for its write changes
	// nothing: the write is what the row is about.
	memoryRenewed(ctx, tracker, "s-1", false, 43200, "REDIS_UNAVAILABLE")
	if rows := tracker.NoDataMemory(); len(rows) != 1 || rows[0].NoDataMemory.Kind != NoDataMemoryRefusalWrite || rows[0].NoDataMemory.Refusals != 1 {
		t.Fatalf("rows after a renewal refusal = %+v, want the write refusal unchanged", rows)
	}
	tracker.Observe(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageNoDataMemoryWritten, Result: observability.ResultSuccess,
		Trace:             observability.TraceFields{StrategyID: "s-1", BusinessID: "2"},
		NoDataMemoryWrite: &observability.NoDataMemoryWriteFacts{Outcome: "APPLIED", Stored: true},
	})
	if rows := tracker.NoDataMemory(); len(rows) != 0 {
		t.Fatalf("rows after a stored write = %+v, want none", rows)
	}
}

// The upkeep rides on the object's row in the columns too, as the positive
// evidence: the Plan whose renewal reached the store most recently, what it
// answered, the lifetime it sets, what the last read found, and how many of
// the object's Plans have upkeep at all.
func TestMemoryUpkeepRidesOnTheObjectRow(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-upkeep"})
	for round := 0; round < DefaultDegradedRounds; round++ {
		memoryRead(ctx, tracker, "s-1", "PER_GROUP")
		memoryRead(ctx, tracker, "s-2", "WHOLE_MEMORY")
		if round == 1 {
			memoryRenewed(ctx, tracker, "s-2", false, 43200, "")
		}
		if round == 2 {
			memoryRenewed(ctx, tracker, "s-1", true, 43200, "")
		}
		degradedRound(ctx, tracker, int64(1000+60*round))
		at.at = at.at.Add(time.Minute)
	}
	rows := anyColumn(tracker)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want the one object", rows)
	}
	upkeep := rows[0].NoDataMemoryUpkeep
	if upkeep == nil || upkeep.Plan.StrategyID != "s-1" || upkeep.Representation != "PER_GROUP" || upkeep.TTLSeconds != 43200 || upkeep.Plans != 2 {
		t.Fatalf("upkeep = %+v, want s-1's -- renewed most recently -- with its shape and lifetime, over 2 Plans", upkeep)
	}
	if upkeep.LastRenewedAt == nil || !upkeep.LastRenewedAt.Equal(now.Add(2*time.Minute)) ||
		upkeep.LastAttemptAt == nil || !upkeep.LastAttemptAt.Equal(now.Add(2*time.Minute)) ||
		upkeep.LastReadAt == nil || !upkeep.LastReadAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("upkeep clocks = %+v, want the third round's", upkeep)
	}
	// Nothing is refused: the upkeep is evidence, not a problem.
	if rows := tracker.NoDataMemory(); len(rows) != 0 {
		t.Fatalf("memory rows = %+v, want none", rows)
	}
}
