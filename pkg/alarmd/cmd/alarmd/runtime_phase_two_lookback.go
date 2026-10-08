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
	"errors"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// lookbackTick is how often due rechecks are looked for: inside the window
// of a rung at a ten-second step, so a rung is tried more than once before
// it yields.
const lookbackTick = 5 * time.Second

// lookbackStanding is what lookback.get answers besides the counts: whether
// this process runs the lookback.
type lookbackStanding struct {
	Running bool `json:"running"`
}

// lookbackOwnership answers the lookback's ownership question from the
// bundle, which is built after the query path the lookback sits on; until
// it is bound nothing is owned, and a first read completed before then is
// dropped as owner_lost.
type lookbackOwnership struct {
	bundle atomic.Pointer[phaseTwoWorkerBundle]
}

func (ownership *lookbackOwnership) bind(bundle *phaseTwoWorkerBundle) {
	ownership.bundle.Store(bundle)
}

func (ownership *lookbackOwnership) owns(queryGroup execution.QueryGroupIdentity) bool {
	bundle := ownership.bundle.Load()
	return bundle != nil && bundle.ownsQueryGroup(queryGroup)
}

// ownsQueryGroup says whether this replica holds a Runner for the Query
// Group now.
func (bundle *phaseTwoWorkerBundle) ownsQueryGroup(queryGroup execution.QueryGroupIdentity) bool {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	_, owned := bundle.runners[queryGroup]
	return owned
}

// supplementRunner is what runs a supplement of one of a Query Group's
// completed Slots. The production Query Group implements it, and must: a
// supplement asked of a Query Group whose runtime does not is never run.
type supplementRunner interface {
	Supplement(context.Context, execution.EvaluationTime, int64, execution.SupplementScope) (execution.SupplementFacts, error)
}

var _ supplementRunner = (*productionPhaseTwoQueryGroup)(nil)

// supplementRunner is the owned Query Group's, when this replica holds its
// Runner and the Runner runs supplements.
func (ownership *lookbackOwnership) supplementRunner(queryGroup execution.QueryGroupIdentity) (supplementRunner, bool) {
	bundle := ownership.bundle.Load()
	if bundle == nil {
		return nil, false
	}
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	lifecycle, owned := bundle.runners[queryGroup]
	if !owned || lifecycle == nil {
		return nil, false
	}
	runner, supported := lifecycle.runner.(supplementRunner)
	return runner, supported
}

// lookbackSupplement runs the lookback's supplements on the Runners of the
// Query Groups they are of, with the late series and the read they came in.
//
// A Query Group whose Slot is executing is tried once more, after half the
// time the rung the read was made at has left, and not after it: a
// supplement never waits for a Slot, and a Slot it could not get past is
// counted flight_busy. A Slot past being supplemented is contract_expired;
// anything else is failed, and logged, since a supplement that failed for a
// reason it could not name is what the log is for.
func lookbackSupplement(
	ownership *lookbackOwnership,
	logger *observability.Logger,
	now func() time.Time,
	wait func(context.Context, time.Duration) error,
) func(context.Context, lookback.SupplementJob) lookback.SupplementOutcome {
	return func(ctx context.Context, job lookback.SupplementJob) lookback.SupplementOutcome {
		runner, found := ownership.supplementRunner(job.QueryGroup)
		if !found {
			return lookback.SupplementOutcome{Refused: lookback.DirectedFailed}
		}
		ctx = scheduler.WithSupplementGuard(access.WithKeptRead(ctx, job.Read), job.Guard)
		scope := execution.SupplementScope{Series: job.Series}
		for retried := false; ; retried = true {
			started := now()
			facts, err := runner.Supplement(ctx, job.EvaluationTime, job.ReadHoldMillis, scope)
			// A call that was not refused for the flight held it this long:
			// the time the Query Group's own Slot waited behind it.
			held := now().Sub(started)
			switch {
			case err == nil:
				return lookback.SupplementOutcome{Ran: true, Facts: facts, Held: held}
			case errors.Is(err, scheduler.ErrSupplementFlightBusy):
				if left := job.Deadline.Sub(now()) / 2; !retried && left > 0 && wait(ctx, left) == nil {
					continue
				}
				return lookback.SupplementOutcome{Refused: lookback.DirectedFlightBusy}
			case errors.Is(err, scheduler.ErrSupplementContractExpired):
				return lookback.SupplementOutcome{Refused: lookback.DirectedContractExpired, Held: held}
			case errors.Is(err, scheduler.ErrSupplementOvertaken):
				return lookback.SupplementOutcome{Refused: lookback.SupplementOvertaken, Held: held}
			default:
				if logger != nil {
					logger.Warn("lookback", "supplement_failed", 0, 0, slog.String("query_group", string(job.QueryGroup)),
						slog.Int64("evaluation_time", int64(job.EvaluationTime)), slog.String("error", err.Error()))
				}
				return lookback.SupplementOutcome{Refused: lookback.DirectedFailed, Held: held}
			}
		}
	}
}

// waitWithin waits for delay, or until ctx ends.
func waitWithin(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// count is how many Query Groups the bound bundle owns: the lookback's
// coverage denominator.
func (ownership *lookbackOwnership) count() int {
	bundle := ownership.bundle.Load()
	if bundle == nil {
		return 0
	}
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	return len(bundle.runners)
}

// buildLookback builds this process's lookback. Nothing configures it and
// it takes no share of memory: it keeps one summary per owned Query Group,
// and its rechecks read through the same query client as the formal reads,
// one lookback permit at a time, never queued behind them and yielded the
// moment a formal query waits. Its sources are the ones a query can be
// compiled from; a fault, which normal running never meets, is logged.
func buildLookback(
	recheck lookback.Recheck,
	flights *scheduler.FlightCoordinator,
	ownership *lookbackOwnership,
	logger *observability.Logger,
	now func() time.Time,
	memory func(bytes uint64) bool,
	holds ...*productionReadHolds,
) (*lookback.Engine, lookbackStanding, error) {
	options := lookbackOptions(recheck, flights, ownership, logger, now, memory)
	if len(holds) > 0 && holds[0] != nil {
		holds[0].bindLookback(&options)
	}
	engine, err := lookback.New(options)
	if err != nil {
		return nil, lookbackStanding{}, err
	}
	return engine, lookbackStanding{Running: true}, nil
}

// lookbackOptions wire the lookback to this process: its query client, its
// permits, its Runner set, its log and the observation memory line its
// series tables grow under.
func lookbackOptions(
	recheck lookback.Recheck,
	flights *scheduler.FlightCoordinator,
	ownership *lookbackOwnership,
	logger *observability.Logger,
	now func() time.Time,
	memory func(bytes uint64) bool,
) lookback.Options {
	return lookback.Options{Now: now, Recheck: recheck, Owns: ownership.owns, Owned: ownership.count,
		Refusals: scheduler.LookbackRefusals, Permit: lookbackPermit(flights), Memory: memory,
		FreePermits: freeQueryPermits(flights),
		Supplement:  lookbackSupplement(ownership, logger, now, waitWithin),
		OnFault: func(reason string, queryGroup execution.QueryGroupIdentity) {
			if logger != nil {
				logger.Warn("lookback", "fault", 0, 0, slog.String("reason", reason), slog.String("query_group", string(queryGroup)))
			}
		}}
}

// lookbackPermit is the lookback's permit from the process's query budget:
// granted only from a permit nobody is waiting for, and yielded the moment a
// formal query has to wait for one.
// freeQueryPermits is how many of the process's query permits are free now:
// the budget less every permit held, the lookback's included.
func freeQueryPermits(flights *scheduler.FlightCoordinator) func() int {
	return func() int {
		occupancy := flights.QueryPermitOccupancy()
		held := occupancy.LookbackInflight
		for _, count := range occupancy.Inflight {
			held += count
		}
		return occupancy.Budget - held
	}
}

func lookbackPermit(flights *scheduler.FlightCoordinator) lookback.Permit {
	return func() (func(), <-chan struct{}, string) {
		permit, refused := flights.TryAcquireLookbackPermit()
		if permit == nil {
			return nil, nil, refused
		}
		return permit.Release, permit.Yield(), ""
	}
}

// lookbackReadEarly is the lookback's report of the objects read before
// their data was complete, as the fleet snapshot carries it; nil without a
// lookback.
func lookbackReadEarly(engine *lookback.Engine, holds ...*productionReadHolds) func() map[string]fleet.ReadEarlyFacts {
	if engine == nil {
		return nil
	}
	return func() map[string]fleet.ReadEarlyFacts {
		readings := engine.ReadEarly()
		facts := make(map[string]fleet.ReadEarlyFacts, len(readings))
		for _, reading := range readings {
			// The samples ride with the values: the suggestion and what it
			// rests on are one read, bounded by the fleet's row.
			row := fleet.ReadEarlyFacts{StepSeconds: reading.StepSeconds,
				CurrentDelaySeconds: reading.CurrentDelaySeconds, SuggestedDelaySeconds: reading.SuggestedDelaySeconds,
				Since: reading.Since}
			if len(holds) > 0 && holds[0] != nil {
				if _, err := holds[0].owner(reading.QueryGroup); err == nil && holds[0].controller.Inspect(reading.QueryGroup).Loaded {
					row.ReadHoldMillis = holds[0].controller.ReadHold(reading.QueryGroup).Milliseconds()
				}
			}
			for _, sample := range reading.Samples {
				row.Samples = append(row.Samples, fleet.ReadEarlySample{EvaluationTime: int64(sample.EvaluationTime),
					FirstReadAgeSeconds: sample.FirstReadAgeSeconds, CompletionAgeSeconds: sample.CompletionAgeSeconds,
					Rung: sample.Rung, ChangedAgeSeconds: sample.ChangedAgeSeconds, Buckets: sample.Buckets,
					PartialRevised: sample.PartialRevised})
			}
			facts[string(reading.QueryGroup)] = row
		}
		return facts
	}
}

// runLookback rechecks due samples until the bundle stops.
func runLookback(ctx context.Context, engine *lookback.Engine) {
	engine.Run(ctx, lookbackTick)
}

// cliLookbackReading is lookback.get's answer: this process's lookback as it
// stands, its counts since the process started, and the recent differing
// rechecks with bounded examples.
type cliLookbackReading struct {
	Scope  string    `json:"scope"`
	ReadAt time.Time `json:"read_at"`
	lookbackStanding
	Stats     *lookback.Stats    `json:"stats,omitempty"`
	ReadHolds *lookbackGroupPage `json:"read_holds,omitempty"`
}

// cliLookbackOperation reads the answering process's lookback. Every replica
// keeps its own; the operation is targetable so each can be read in turn.
func cliLookbackOperation(engine *lookback.Engine, standing lookbackStanding, holds ...*productionReadHolds) obchannel.Operation {
	one, maxLimit := int64(1), int64(200)
	return obchannel.Operation{ID: "lookback.get",
		Summary:       "读取实际回答进程的晚到数据回看：拥有的查询组有新鲜测量的覆盖率（覆盖数 ÷（拥有数 − 从未有过完整首读的组数），目标 100%；从未有过完整首读的组单列计数，并按查询组列出不完整首读的次数，最多 32 个）；按来源给出每档复查与上一次读有变化的窗口数、按变化类别的桶数、到齐时刻的分布与最大值、未观测比例（unobserved 占已结束样本）、深探结果（干净、有变化、未读到）与深探才发现迟到的样本比例（probe_changed 占已结束样本，不进到齐分布）、首读完整但为空的样本后来是否到数及其到齐时刻、按事实分的四类样本数（整窗读早、部分序列迟到、完整、未分类：序列表被内存安全线拒绝而分不出，按原因计）与连续两次整窗读早的查询组（read_early：当前有效 time_delay、建议值（上界）、依据的样本与变化的桶）、各深度的查询组数与平均休息期、首读与复查的次数和字节（额外查询量）、取不出回看的样本数、让出与许可拒绝；部分序列迟到的查询组逐个 Slot 定向复查并补充检测（supplements：每组的迟到档、窗口结局——已补、无迟到、Slot 在跑未补、合同已过期、失败、未观测按原因——补充里每对（Plan、序列）的结局与补上的点数、覆盖率＝已补/(已补＋未观测)；按来源的合计与定向复查字节）；到齐最晚的查询组与最近有变化的复查；可指定实例。",
		EvidenceScope: "process", Targetable: true, Fields: map[string]obchannel.Field{
			"after": {Type: "string", MaxLength: 256, Description: "下一页用 read_holds.next；查询组按 ID 排序。"},
			"limit": {Type: "integer", Minimum: &one, Maximum: &maxLimit, Description: "每页查询组数，默认 32，最多 200。"},
		},
		OutputSchema: obchannel.SchemaOf(cliLookbackReading{}),
		Limits:       map[string]any{"redis_commands": 0, "scope": "answering_replica", "recent": 32, "latest": 32},
		Run: func(_ context.Context, params obchannel.Params) obchannel.Outcome {
			reading := cliLookbackReading{Scope: "answering_replica", ReadAt: time.Now().UTC(), lookbackStanding: standing}
			if len(holds) > 0 && holds[0] != nil {
				page := holds[0].groupPage(engine, params.String("after"), params.Int("limit", 32))
				reading.ReadHolds = &page
			}
			if engine != nil {
				stats := engine.Stats()
				if len(holds) > 0 && holds[0] != nil {
					stats = productionLookbackStats(engine, holds[0])
				}
				reading.Stats = &stats
			}
			outcome := obchannel.Outcome{Value: reading, Complete: true, Limitations: []string{
				"Counts are this process's since it started; use meta.answered_by, and target each replica for the deployment.",
				"Each rung is compared with the read before it; a window is complete at the last rung that changed, or at the first read when none did. Only rechecks with outcome compared are windows observed; every other outcome is a window not observed, not a window that did not change.",
				"Rungs are at 1.5, 3.5, 7.5, 15.5, 31.5 and 63.5 of the Query Group's data steps. Each Query Group learns from its own samples how many to read and how long to rest between samples, at most an hour; a source only sums its groups. A recheck reads and compares only the window's last 65 steps - the whole of a shorter window - from the query's own lookback before them.",
				"A Query Group's first sample and one in four after it are read once more at the deepest rung after the rungs the group reads; data found there makes that sample probe_changed, with no completion, and the group reads every rung and settles from its next sample. A window still changing at the deepest rung has its completion counted there, and lateness past it is not measured; its class is unclassified (unsettled), not complete, because no later read agrees with that last one.",
				"Each completed sample is classed by the facts of its series against the first read, as its data settled: the read of its last change, which a later rung read again the same, so a change that came back is not one; values are compared to one part in 2^28 (about 3.7e-9 of the value), so reads that differ only in the order the store summed them are one read: window_read_early when the first read was empty and data came later, or a series it had came back with other points or values or not at all (the strategy's time_delay moves the read; a value revised after it was judged is not judged again); partial_revised when some series it had came back with other points or values and a value, and others with a value came back as they were - the series were late, not the window, and a series zero or without a value in both reads decides nothing (still carried by the read_early run, marked partial_revised on its sample); series_late when every series it had came back as it was and others came later (supplementary detection's); unclassified when a rung changed and its series could not be compared because the memory line refused a series table (memory_refused), or the deepest rung still changed so no later read says the data settled (unsettled) (counted by reason under unclassified, not a fault); complete otherwise. read_early lists the Query Groups read early - read early or partially revised - in two of their latest three classified samples, with the time_delay they run under and the one that would have read their samples complete: the largest completion past the first read added, aligned up to the step as a strategy's time_delay is compiled. A completion is the age of the recheck that first read the data whole, so the suggestion is an upper bound.",
				"A series_late Query Group has every Slot read again at the rung its late series were seen at - the Slot's frozen query whole, not a tail - and the series that read has and the first read did not are supplemented at the Slot on the read they came in: the query service is asked once for both. A Slot of more than one physical query is not read (unobserved, multi_query). One Slot of a Query Group at a time, the oldest first; a supplement never waits for its Query Group's Slot, is tried once more within its rung when that Slot is executing, and is counted flight_busy otherwise. Coverage is supplemented Slots over supplemented and unobserved ones; Slots with nothing late are in neither. The series outcomes are the supplement's own: admitted, crossed_t (State already at the Slot or past it), no_data_fact (its no-data group recorded absent at the Slot), config_drift, input_incomplete, withheld.",
				"A sample waiting for its deep recheck does not hold its group's next sample back: a punctual Query Group settles at 1 to 1.25 rechecks an hour, about a fifth of them deep (simulated: 1.23 at a ten-second step, 1.21 at a minute, 1.02 at five minutes - a group rests from its first rung, 1.5 steps after its read, and waits for its next first read).",
				"A recheck reads through the same query service as the first read. The query service keeps no result cache by its source (its caches hold routing metadata and reload coordination); the deployed version is read from its workload image, not from here. A storage-layer cache that answers until its next refresh, such as a search engine's request cache, is a known boundary: it can return the first read again.",
			}}
			if reading.ReadHolds != nil && reading.ReadHolds.Next != "" {
				outcome.Complete = false
				outcome.Next = []obchannel.Call{{Operation: "lookback.get", Params: obchannel.Params{"after": reading.ReadHolds.Next, "limit": json.Number(strconv.Itoa(params.Int("limit", 32)))}, Reason: "read the next owned query groups"}}
			}
			return outcome
		}}
}

// lookbackLateSeries is what the lookback's supplements could not recover,
// as the fleet snapshot carries it. Nil without a lookback.
func lookbackLateSeries(engine *lookback.Engine, holds ...*productionReadHolds) func() (map[string]fleet.LatePastRoundFacts, map[string]fleet.LateSeriesMissedFacts) {
	if engine == nil {
		return nil
	}
	return func() (map[string]fleet.LatePastRoundFacts, map[string]fleet.LateSeriesMissedFacts) {
		past, residual := lateSeriesFacts(engine.LatePastRound(), engine.ResidualMisses())
		if len(holds) > 0 && holds[0] != nil {
			for qg, reading := range past {
				id := execution.QueryGroupIdentity(qg)
				if _, err := holds[0].owner(id); err == nil && holds[0].controller.Inspect(id).Loaded {
					reading.ReadHoldMillis = holds[0].controller.ReadHold(id).Milliseconds()
					past[qg] = reading
				}
			}
		}
		return past, residual
	}
}

// lateSeriesFacts is the lookback's readings as the fleet rows carry them:
// the objects whose late series had crossed their Slots window after window,
// and the residual misses of the windows recovered in part, by object, with
// their samples.
func lateSeriesFacts(pastRound []lookback.LatePastRoundReading, residual []lookback.ResidualMissReading) (
	map[string]fleet.LatePastRoundFacts, map[string]fleet.LateSeriesMissedFacts) {
	past := make(map[string]fleet.LatePastRoundFacts, len(pastRound))
	for _, reading := range pastRound {
		row := fleet.LatePastRoundFacts{StepSeconds: reading.StepSeconds, CurrentDelaySeconds: reading.CurrentDelaySeconds,
			SuggestedDelaySeconds: reading.SuggestedDelaySeconds, Since: reading.Since}
		for _, sample := range reading.Samples {
			row.Samples = append(row.Samples, fleet.LatePastRoundSample{EvaluationTime: int64(sample.EvaluationTime),
				Rung: sample.Rung, SeenAgeSeconds: sample.SeenAgeSeconds, OnTimeSeries: sample.OnTimeSeries, LateSeries: sample.LateSeries,
				CrossedSeries: sample.CrossedSeries})
		}
		past[string(reading.QueryGroup)] = row
	}
	missed := make(map[string]fleet.LateSeriesMissedFacts, len(residual))
	for _, reading := range residual {
		row := fleet.LateSeriesMissedFacts{Windows: reading.Windows, CrossedSeries: reading.CrossedSeries, Since: reading.Since}
		for _, sample := range reading.Samples {
			row.Samples = append(row.Samples, fleet.LateSeriesMissedSample{EvaluationTime: int64(sample.EvaluationTime),
				OnTimeSeries: sample.OnTimeSeries, LateSeries: sample.LateSeries, AdmittedSeries: sample.AdmittedSeries,
				CrossedSeries: sample.CrossedSeries})
		}
		missed[string(reading.QueryGroup)] = row
	}
	return past, missed
}
