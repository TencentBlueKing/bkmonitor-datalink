// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Every other check in this file is static: field names against the Go types,
// containers, wiring, wording. None of them run the page, so none of them can
// see a ReferenceError -- and one shipped. The object table rendered nothing
// but "a is not defined" for two releases while every test here was green.
//
// This one executes the render functions. It is the only check that can fail on
// a page that throws.
//
// The fixture is built from the Go types rather than written as JSON, so the
// shapes cannot drift from what the API sends without this failing to compile.
func TestTheRenderFunctionsRunWithoutThrowing(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		// Loudly. A skipped check is no check, and the failure it exists for is
		// invisible to everything else here.
		t.Skip("node is not on PATH, so the page's logic is NOT executed by this run -- " +
			"only the static checks above ran, and a ReferenceError would pass all of them")
	}

	at := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	anomaly := func(id string, mutate func(*fleet.Anomaly)) fleet.Anomaly {
		item := fleet.Anomaly{
			QueryGroup: id, Replica: "bk-monitor-alarmd-trigger-5bdb679ddf-abcde",
			Kind: "DEGRADED_RUN", ReasonCode: "COMPLETED_WITH_UNAVAILABLE",
			Since: at.Add(-time.Hour), SinceFrom: fleet.SinceSnapshotContinuity,
			Strategies: []fleet.StrategyRef{{StrategyID: "1234", BusinessID: "7"}},
		}
		if mutate != nil {
			mutate(&item)
		}
		return item
	}
	// One row of every shape the renderer branches on. A row shape that is not
	// here is a branch this check does not execute.
	rows := []fleet.Anomaly{
		anomaly("qg-plain", nil),
		anomaly("qg-reason", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
		}),
		anomaly("qg-failure", func(item *fleet.Anomaly) {
			item.Failure = &fleet.FailureRef{Stage: "provider", Category: "source_backend",
				Code: "QUERY_UNAVAILABLE", Detail: "http_status=400"}
		}),
		anomaly("qg-restored", func(item *fleet.Anomaly) {
			item.SinceFrom = fleet.SinceRestoredLastFull
		}),
		anomaly("qg-preexisting", func(item *fleet.Anomaly) {
			item.SinceFrom = fleet.SinceProcessStart
		}),
		anomaly("qg-stalled", func(item *fleet.Anomaly) {
			item.Stalled, item.FailingSince = true, at.Add(-2*time.Hour)
		}),
		// Anomalous for an hour, saying its current reason for twelve minutes
		// and twelve rounds: the two clocks read differently, and the row says
		// both.
		anomaly("qg-two-clocks", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "QUERY_TIMEOUT"
			item.ReasonSince, item.Consecutive = at.Add(-12*time.Minute), 12
		}),
		// Saying the same reason since it became anomalous: one clock, said once.
		anomaly("qg-one-clock", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "QUERY_TIMEOUT"
			item.ReasonSince, item.Consecutive = at.Add(-time.Hour), 60
		}),
		// A round failing on the same Slot three times, with the error's own
		// words: what a reader had to open a window and wait for.
		anomaly("qg-stuck-slot", func(item *fleet.Anomaly) {
			item.ReasonCode = "error"
			item.LastError = &fleet.LastError{Text: "alarmd state: gap guard conflict: expected 41 got 43",
				Type: "*errors.errorString", EvaluationTime: at.Add(-3 * time.Minute).Unix(), At: at.Add(-time.Minute), Attempts: 3}
		}),
		anomaly("qg-blocked", func(item *fleet.Anomaly) {
			item.Kind, item.ReasonCode, item.Strategies = "BLOCKED_RUN", "source_blocked", nil
		}),
		// A round whose commit failed while the state store was reloading,
		// in the words the store's client wrote, after an hour of healthy
		// completions: the reading names Redis from the text, the commit
		// operation, the retry, and the last success.
		anomaly("qg-redis-commit", func(item *fleet.Anomaly) {
			item.ReasonCode = "error"
			item.LastError = &fleet.LastError{Text: "alarmd progress: commit: LOADING Redis is loading the dataset in memory",
				Type: "*fmt.wrapError", EvaluationTime: at.Add(-2 * time.Minute).Unix(), At: at.Add(-90 * time.Second), Attempts: 2, Operation: "commit"}
			item.LastHealthyAt = at.Add(-time.Hour)
		}),
		anomaly("qg-cooldown", func(item *fleet.Anomaly) {
			item.Kind = "QUERY_COOLDOWN"
			item.QueryCooldown = &observability.QueryCooldownFacts{
				Until: at.Add(time.Hour), LastQueryAt: at.Add(-time.Minute), Failures: 5}
			item.Failure = &fleet.FailureRef{Stage: "provider", Category: "source_backend",
				Code: "QUERY_UNAVAILABLE", Detail: "transport=timeout"}
		}),
		// Same pool, same code, and the backend answered: it read the query and
		// refused it. A live deployment held 350 of these filed as the backend's.
		anomaly("qg-rejected", func(item *fleet.Anomaly) {
			item.Kind = "QUERY_COOLDOWN"
			item.QueryCooldown = &observability.QueryCooldownFacts{
				Until: at.Add(time.Hour), LastQueryAt: at.Add(-time.Minute), Failures: 40}
			item.Failure = &fleet.FailureRef{Stage: "provider", Category: "source_backend",
				Code: "QUERY_UNAVAILABLE", Detail: "response=status_space_table_id_field_is_not_exists"}
			// And an internal aggregation conflict beside the refusal: the
			// second fact, listed under DEFECT as well, never hidden by the
			// HTTP status.
			item.Internal = &fleet.FailureRef{Stage: "execute", Category: "completion_contract",
				Code: "GAP_SCOPE_REASON_CONFLICT", Detail: "input_a=QUERY_UNAVAILABLE input_b=QUERY_TIMEOUT"}
		}),
		anomaly("qg-window-filling", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Coverage = &fleet.HistoryCoverage{Levels: 3, Short: 1,
				WorstValid: 8, WorstRequired: 9, ShortRounds: 2}
		}),
		anomaly("qg-window-never", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Coverage = &fleet.HistoryCoverage{Levels: 3, Short: 2,
				WorstValid: 2, WorstRequired: 14, ShortRounds: 40}
		}),
		anomaly("qg-window-complete", func(item *fleet.Anomaly) {
			item.Coverage = &fleet.HistoryCoverage{Levels: 3}
		}),
		// One short window among many. Same verdict as qg-window-never and a
		// completely different amount of broken -- one series inside a strategy
		// against a strategy that cannot be detected at all. Every number the
		// row used to render is identical between the two, because the worst
		// pair is by construction one window and the rounds counter does not
		// say how many windows earned it.
		// A window that is complete under a reason that says it is not.
		//
		// The reason is held over: a Level judged WARMING or GAPPED forces that
		// verdict onto every later evaluation until the window is full at the
		// last processed record, while the counts beside it stay live. This row
		// used to render 检测窗口完整 next to a cause of HISTORY_GAPPED -- the
		// page stating both halves of a contradiction and marking neither.
		anomaly("qg-window-held-complete", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_GAPPED"
			item.Coverage = &fleet.HistoryCoverage{Levels: 3, Guarded: 3}
		}),
		// Held over and still short: the conclusion about the counts stands, the
		// reason under it may not.
		anomaly("qg-window-held-short", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Coverage = &fleet.HistoryCoverage{Levels: 4, Short: 2, Guarded: 1,
				WorstValid: 2, WorstRequired: 14, ShortRounds: 40}
		}),
		// The two halves of "this window will never fill", identical in every
		// count that was ever published about them: same shortfall, same run,
		// same reason, same completeness. The page named the first as the likely
		// cause of both and sent whoever read the second to edit a strategy
		// whose aggregation dimensions were never the problem.
		anomaly("qg-window-churn", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			// Not moving: the same worst count for twelve rounds.
			item.Coverage = &fleet.HistoryCoverage{Levels: 9, Short: 4,
				WorstValid: 2, WorstRequired: 9, ShortRounds: 40,
				Fresh: 4, ShortFresh: 4, FreshRounds: 40,
				Abnormal: 3, AbnormalOnIncomplete: 3,
				PreviousKnown: true, PreviousWorstValid: 2, NoProgressRounds: 12}
		}),
		anomaly("qg-window-stale-data", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			// Filling: one more valid point than last round.
			item.Coverage = &fleet.HistoryCoverage{Levels: 9, Short: 4,
				WorstValid: 2, WorstRequired: 9, ShortRounds: 40,
				PreviousKnown: true, PreviousWorstValid: 1}
		}),
		// Every short window fresh for a single round, which is what the round
		// after a strategy edit looks like: a StateGeneration change re-keys
		// every series at once and they all load nothing. Reported as churn it
		// would accuse a strategy of the edit that was just made to it.
		anomaly("qg-window-rekeyed", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Coverage = &fleet.HistoryCoverage{Levels: 9, Short: 4,
				WorstValid: 2, WorstRequired: 9, ShortRounds: 40,
				Fresh: 9, ShortFresh: 4, FreshRounds: 1}
		}),
		anomaly("qg-window-lopsided", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Coverage = &fleet.HistoryCoverage{Levels: 999, Short: 1,
				WorstValid: 2, WorstRequired: 14, ShortRounds: 40}
		}),
		// The two rows a live page showed side by side. The GAPPED one carried
		// the churning-series wording because the row note read coverage and
		// never the reason -- the same defect as the summary count, in a
		// second place, and this fixture is what executes it.
		anomaly("qg-gapped-intermittent", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_GAPPED"
			item.Coverage = &fleet.HistoryCoverage{Levels: 1, Short: 1,
				WorstValid: 5, WorstRequired: 9, ShortRounds: 29}
		}),
		anomaly("qg-drift", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "CONFIG_DRIFT", "CONFIG_DRIFT"
		}),
		// The shape of six live strategies: CONFIG_DRIFT on every round for
		// 29 rounds, carried by a history guard established once, over a
		// window still short. Read bare it sent a reader to check whether the
		// strategy was being edited.
		anomaly("qg-guard-held", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "CONFIG_DRIFT"
			item.ReasonSince, item.Consecutive = at.Add(-29*time.Minute), 29
			item.Coverage = &fleet.HistoryCoverage{Levels: 3, Short: 1, Guarded: 3,
				WorstValid: 5, WorstRequired: 9, ShortRounds: 29}
		}),
		anomaly("qg-offhours", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "EFFECTIVE_TIME_INACTIVE"
		}),
		anomaly("qg-skipped", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "GAP_SKIPPED"
			item.Coverage = &fleet.HistoryCoverage{Levels: 1, Short: 1,
				WorstValid: 4, WorstRequired: 9, ShortRounds: 6}
		}),
		anomaly("qg-gapped-fresh", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_GAPPED"
			item.Coverage = &fleet.HistoryCoverage{Levels: 1, Short: 1,
				WorstValid: 5, WorstRequired: 9, ShortRounds: 3}
		}),
		anomaly("qg-window-starved", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Coverage = &fleet.HistoryCoverage{Levels: 3, Short: 2, Empty: 2,
				WorstRequired: 14, ShortRounds: 40, EmptyRounds: 40,
				Unusable: 2, UnusableReason: "REQUIRED_VALUE_MISSING"}
		}),
		// Data that stopped: rounds completing, nothing coming back.
		anomaly("qg-no-data", func(item *fleet.Anomaly) {
			item.Kind, item.ReasonCode = fleet.KindNoData, "FULL_EMPTY_COMPLETED"
		}),
		// Where the object is in its cycle, from the due index. One waiting,
		// one late within its period, one that has missed a turn under a
		// backend situation (and is therefore this deployment's line, not the
		// backend's), one never evaluated since takeover.
		anomaly("qg-waiting", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Wake = &fleet.WakeFacts{Known: true, DueAt: at.Add(40 * time.Second), IntervalSeconds: 60}
		}),
		anomaly("qg-late", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Wake = &fleet.WakeFacts{Known: true, DueAt: at.Add(-12 * time.Second), IntervalSeconds: 60}
		}),
		anomaly("qg-missed-turn", func(item *fleet.Anomaly) {
			item.CauseReason = "QUERY_TIMEOUT"
			item.Wake = &fleet.WakeFacts{Known: true, DueAt: at.Add(-4 * time.Minute), IntervalSeconds: 60}
		}),
		anomaly("qg-never", func(item *fleet.Anomaly) {
			item.SinceFrom = fleet.SinceRestoredLastFull
			item.Wake = &fleet.WakeFacts{Known: false}
		}),
	}
	fleet.Attribute(rows, at)
	// The first screen, folded from the same rows by the same Go code the route
	// uses, so the sentences the page renders are checked against counts that
	// cannot drift from what the server sends. Three objects nobody can speak
	// for and a stale replica, so the observation-gap line has both a fold with
	// objects and one without.
	// A retained skip: an object that skipped Slots past the replay window an
	// hour ago and has run normally since. It is under no column and on the
	// first screen anyway, with a row of its own.
	// And the two standings: the leader unable to bring the activation to
	// the publication for two hours (the shape a running deployment was in
	// for half a day), and one replica's open alert set past its bound.
	failingFor := (2 * time.Hour).Seconds()
	// The leader's rebalance round the split standing is decided on: 527
	// against 452 is 15% of the even 489, past the scheduler's 5%, so the
	// round moves a batch of nine -- and this round published them all.
	rebalance := &fleet.RebalanceFacts{PlannedAt: at.Add(-20 * time.Second), ReadyWorkers: 2, Assigned: 979, Target: 489,
		MostOwned: 527, LeastOwned: 452, MostOwnedBy: "bk-monitor-alarmd-trigger-5bdb679ddf-abcde",
		LeastOwnedBy: "bk-monitor-alarmd-trigger-5bdb679ddf-fghij", Batch: 9, PlannedMoves: 9, StopSpreadPercent: 5,
		PublishedMoves: 9}
	activation := &fleet.ActivationFacts{
		Applied: "bdc6ffcb0000000000000000", Published: "e7a1b2c30000000000000000",
		Behind: true, BehindBeyondBound: true, ConsecutiveFailures: 120,
		FailingSecondsThisProcess: &failingFor,
		FailureStage:              "schedule_cutover", FailureClass: "schedule_conflict",
		LastFailure: "alarmd controlplane: schedule activation conflict",
	}
	degradations := []fleet.Degradation{
		{Kind: fleet.DegradationActivationBehind, Replica: "bk-monitor-alarmd-trigger-5bdb679ddf-abcde"},
		{Kind: fleet.DegradationOpenAlertSetStale, Replica: "bk-monitor-alarmd-trigger-5bdb679ddf-fghij"},
		// The stale source with the failure behind it: a catalogue that would
		// not validate because one plan asks for more retention than the
		// deployment keeps. The line says that, not the kind name.
		{Kind: fleet.DegradationControlSourceStale, Replica: "bk-monitor-alarmd-trigger-5bdb679ddf-abcde",
			Stage: "validate_catalog", Text: "plan retention 60h13m exceeds catalog retention 24h13m"},
	}
	// The demoted pool: a refused object that also skipped Slots while
	// there, two minutes ago. The shape of 346 live objects whose records
	// the page filed as this deployment giving up for want of capacity.
	demoted := []fleet.Anomaly{anomaly("qg-demoted-rejected", func(item *fleet.Anomaly) {
		item.Kind = "QUERY_COOLDOWN"
		item.QueryCooldown = &observability.QueryCooldownFacts{
			Until: at.Add(time.Hour), LastQueryAt: at.Add(-time.Minute), Failures: 23}
		item.Failure = &fleet.FailureRef{Stage: "provider", Category: "source_backend",
			Code: "QUERY_UNAVAILABLE", Detail: "response=status_space_table_id_field_is_not_exists"}
	})}
	fleet.Attribute(demoted, at)
	retained := &fleet.View{Unknown: 3, Gaps: []fleet.Gap{{Kind: fleet.GapSnapshotStale, Replica: "pod-b"}},
		Demoted: demoted,
		GapSkips: map[string]fleet.SkippedSpan{
			"qg-skipped-hour-ago": {
				FirstSlot: at.Add(-90 * time.Minute).Unix(), LastSlot: at.Add(-70 * time.Minute).Unix(),
				Slots: 20, At: at.Add(-time.Hour), Replica: "bk-monitor-alarmd-trigger-5bdb679ddf-abcde"},
			// Healthy now and losing rounds now, on the replica that has been
			// up for hours: a loss in progress by some mechanism that is not
			// the restart's.
			"qg-losing-now": {
				FirstSlot: at.Add(-4 * time.Minute).Unix(), LastSlot: at.Add(-3 * time.Minute).Unix(),
				Slots: 6, At: at.Add(-3 * time.Minute), Replica: "bk-monitor-alarmd-trigger-5bdb679ddf-abcde",
				Strategies: []fleet.StrategyRef{{StrategyID: "8709", BusinessID: "9"}}, IntervalSeconds: 10,
				Reason: "QUERY_PERMIT_DEADLINE", ReasonCategory: "admission"},
			"qg-demoted-rejected": {
				FirstSlot: at.Add(-5 * time.Minute).Unix(), LastSlot: at.Add(-2 * time.Minute).Unix(),
				Slots: 3, At: at.Add(-2 * time.Minute), Replica: "bk-monitor-alarmd-trigger-5bdb679ddf-abcde"},
			// Skipped a minute after its replica started five minutes ago: the
			// restart's catch-up, the shape every rollout produces.
			"qg-restart-catchup": {
				FirstSlot: at.Add(-5 * time.Minute).Unix(), LastSlot: at.Add(-4 * time.Minute).Unix(),
				Slots: 6, At: at.Add(-4 * time.Minute), Replica: "bk-monitor-alarmd-trigger-5bdb679ddf-fghij", IntervalSeconds: 10},
		},
		// The replicas' starts, which the restart grace is read against:
		// fghij restarted five minutes ago (the rollout), abcde two hours ago.
		PerReplica: []fleet.ReplicaView{
			{Replica: "bk-monitor-alarmd-trigger-5bdb679ddf-abcde", StartedAt: at.Add(-2 * time.Hour)},
			{Replica: "bk-monitor-alarmd-trigger-5bdb679ddf-fghij", StartedAt: at.Add(-5 * time.Minute)},
		},
		Activation: activation, ActivationReplica: "bk-monitor-alarmd-trigger-5bdb679ddf-abcde",
		// The leader's rebalance round: the replica that restarted five
		// minutes ago holds less than the one that has been up for hours,
		// past the scheduler's tolerance, and the build only plans the
		// moves. The third standing is built from this and nothing else.
		Rebalance: rebalance, RebalanceReplica: "bk-monitor-alarmd-trigger-5bdb679ddf-abcde",
		Degradations: degradations}
	columns := [][]fleet.Anomaly{rows, demoted}
	checks := fleet.ReportChecks(columns, nil, retained, at)
	todo := fleet.SummarizeTodo(checks, columns, retained, at)
	rows = append(rows, fleet.UnderCheck(fleet.CheckDetectionAbandoned, "", retained, at)...)
	rows = append(rows, fleet.UnderCheck(fleet.CheckQueryTargetMissing, "", retained, at)...)
	// The barest row the API can send: every omitempty field absent. It goes in
	// after Attribute so it keeps its empty attribution, because a fixture where
	// every row has every field cannot catch a property read on a field that is
	// sometimes not there -- which is the other half of what a render throws on.
	rows = append(rows, fleet.Anomaly{QueryGroup: "qg-bare", Replica: "r-1", Kind: "DEGRADED_RUN"})

	// The build the deployment runs. On one replica row and absent on the
	// other, so the cell renders both a reported and an unreported build.
	build := fleet.BuildFacts{Version: "0.2.4506", Commit: "62ee924d00000000", SchemaVersion: "v3"}
	replicas := []fleet.ReplicaView{
		// One replica reporting no undecidable objects, so the render is
		// executed on both a present and an absent count. A fixture where every
		// row carries every field cannot catch a read on one that is sometimes
		// missing, which is half of what a render throws on.
		{Replica: "bk-monitor-alarmd-trigger-5bdb679ddf-abcde", Owned: 527, Healthy: 460,
			Anomalies: 53, Demoted: 14, AgeSeconds: 3, UptimeSeconds: 7200, StartedAt: at.Add(-2 * time.Hour),
			Ours: 8, External: 41, Build: &build},
		// This one restarted five minutes ago and holds less: the shape the
		// split standing is read on. And it published a cut list, so the
		// completeness sentence renders both halves: every object has a
		// state, the detail is a sample.
		{Replica: "bk-monitor-alarmd-trigger-5bdb679ddf-fghij", Owned: 452, Healthy: 384,
			Anomalies: 33, Demoted: 19, Undecidable: 12, ByDesign: 4, AgeSeconds: 4,
			UptimeSeconds: 300, StartedAt: at.Add(-5 * time.Minute), Ours: 5, External: 26, Truncated: true},
	}
	lastExit := at.Add(-2 * time.Minute)
	fixture := map[string]any{
		"anomalies": rows,
		"checks":    checks,
		"todo":      todo,
		"summary": fleet.Summary{
			ByKind:   fleet.Distribution{Top: []fleet.Count{{Value: "DEGRADED_RUN", Count: 7}}, Distinct: 1},
			ByReason: fleet.Distribution{Top: []fleet.Count{{Value: "COMPLETED_WITH_UNAVAILABLE", Count: 7}}, Distinct: 1},
			ByBusiness: fleet.Distribution{Top: []fleet.Count{{Value: "7", Count: 7}},
				Distinct: 9000, TailObjects: 40},
			ByFailureDetail: fleet.Distribution{
				Top: []fleet.Count{{Value: "http_status=400", Count: 1}}, Distinct: 1},
			Strategies: 7, Ours: 3, External: 4, Unattributed: 1, OursUnclassified: 1,
			Onset: fleet.Onset{LastHour: 2, LastDay: 3, Older: 3,
				NewestSince: at.Add(-time.Minute), OldestSince: at.Add(-40 * time.Hour)},
			WindowNeverFills: 3, WindowSeriesChurn: 1,
		},
		// A filter narrowing the list, a replica that could not publish it, and
		// both at once. The first used to be reported as the second.
		"page_tail_cases": []map[string]any{
			{"name": "filtered", "total": 1, "objects": map[string]any{
				"anomalies_total": 16, "filtered": true,
				"summary": map[string]any{"partial": false}}},
			{"name": "truncated", "total": 50, "objects": map[string]any{
				"anomalies_total": 900, "filtered": false,
				"summary": map[string]any{"partial": true}}},
			{"name": "plain", "total": 16, "objects": map[string]any{
				"anomalies_total": 16, "filtered": false,
				"summary": map[string]any{"partial": false}}},
		},
		// A console that is configured, one that is not, and a reference with
		// no business id -- the console needs one to resolve the space, so a
		// link without it lands on an error page.
		"strategy_link_cases": []map[string]any{
			{"name": "configured", "base": "https://monitor.example",
				"strategy": fleet.StrategyRef{StrategyID: "1854", BusinessID: "7"}},
			{"name": "nobiz", "base": "https://monitor.example",
				"strategy": fleet.StrategyRef{StrategyID: "1854"}},
			{"name": "unconfigured", "base": "",
				"strategy": fleet.StrategyRef{StrategyID: "1854", BusinessID: "7"}},
		},
		// A 24-hour window and a 15-minute one. The first ends at the same
		// wall-clock time it started, which is what made it render empty.
		"range_cases": []map[string]any{
			{"name": "day", "start": at.Add(-24 * time.Hour).UnixMilli(), "end": at.UnixMilli()},
			{"name": "short", "start": at.Add(-15 * time.Minute).UnixMilli(), "end": at.UnixMilli()},
		},
		"health": fleet.HealthResponse{
			// The columns add up to Covered on purpose: the page prints that
			// equation and it is the only thing a reader has that says the
			// split is complete. A fixture that does not add up cannot tell a
			// page that dropped a column from one that is fine.
			Health: "UNKNOWN", Covered: 979, Determined: 979, Unknown: 0, Healthy: 844,
			AnomaliesTotal: 86, DemotedTotal: 33, UndecidableTotal: 12, ByDesignTotal: 4,
			DemotedDue: 2, DemotionEntries: 40, DemotionExits: 7, PerReplica: replicas,
			// Both counted replicas on one build: the line says so in one
			// sentence. The other shapes -- a rollout in progress, a build that
			// reports none -- are variants below.
			Builds: []fleet.BuildGroup{{Build: build, Replicas: []string{replicas[0].Replica, replicas[1].Replica}}},
			// The standings the verdict was decided on, whole, so the why line
			// and the first sentence can name them.
			Degradations: degradations, Activation: activation,
			ActivationReplica: "bk-monitor-alarmd-trigger-5bdb679ddf-abcde",
			Rebalance:         rebalance, RebalanceReplica: "bk-monitor-alarmd-trigger-5bdb679ddf-abcde",
			// The tracker writes the exit count and the exit time on adjacent
			// lines, so a deployment with exits always has this. Without it here
			// the fixture described a deployment that cannot exist -- and the page
			// rendered it as 最近一次出池 in the year 1, because omitempty does
			// nothing for a struct and the zero time is truthy.
			LastDemotionExit: &lastExit,
			// Sub-minute, because that is the range this line claims is normal
			// and the range its renderer could not express: it floored to whole
			// minutes and printed "0 分钟", which is also what it prints when
			// there is nothing overdue at all. The fixture carried no value here
			// at all before, so the clause never ran and the defect shipped.
			DemotedDueOldestSeconds: 40,
			// Unattributed beside a coverage gap. This cell used to claim the
			// verdict unconditionally, and with a gap present the banner above it
			// names a different cause -- one screen, two answers.
			Unattributed: 8,
			Gaps:         []fleet.Gap{{Kind: fleet.GapUndetermined}},
			// Spans nothing ever evaluated. In no column and in no total, which
			// is the whole difficulty -- the objects are running now and every
			// other number on the panel says so, correctly.
			PrunedSkips: []fleet.PrunedSkipRef{
				{QueryGroup: "qg-pruned-long", SpanSeconds: 5400, At: at.Add(-20 * time.Minute),
					DiscardedSlot: at.Add(-90 * time.Minute).Unix()},
				{QueryGroup: "qg-pruned-short", SpanSeconds: 180, At: at.Add(-time.Hour)},
			},
			// Nothing overdue, with the dispatch suppression that makes that zero
			// mean something. This is the branch a healthy deployment renders and
			// the one nobody had ever executed.
			Overdue:  &fleet.OverdueFacts{Total: 0},
			Dispatch: &fleet.DispatchSuppression{Parked: 1993, Skipped: map[string]uint64{}},
			// The census the first sentence is built from: keeping up, with a
			// few late and one never evaluated.
			Schedule: &fleet.ScheduleCensus{Waiting: 1900, Cooling: 33, Late: 12, Overdue: 0, Never: 1,
				Completed1h: 9000, OnTime1h: 8964, Completed6h: 54000, OnTime6h: 53700,
				HeldBack1h: 120, HeldBackOnTime1h: 118},
			// Capacity, which this check had never executed: the fixture carried
			// no capacity, so renderCapacity returned at its first guard and the
			// busiest computed panel on the page was covered by nothing. A null
			// dereference in it reached a live deployment.
			Capacity: &fleet.CapacityView{
				Replicas: 2, PermitsHeld: 3, PermitBudget: 16, PermitSeconds: 1200.5,
				Waiting: 0, QueueBudget: 256,
				// The shape a live deployment is in: nothing queued at this
				// instant, and most queries having waited at some point. The two
				// gauges alone read as headroom, which is what this fixture is
				// here to stop the page concluding.
				PermitAcquires: 40000, PermitWaits: 22400,
				MemoryUsed: 3 << 30, MemoryLimit: 16 << 30, MemorySource: "pod_limit",
				MemoryLimitKnown: true, ThrottledKnown: true, ThrottledSeconds: 0.0017,
				CPUSeconds: 4820.25, CPUCores: 8, CPUSource: "container CPU limit",
				Budgets:    map[string]uint64{"events": 65536, "series": 524288},
				Rejections: map[string]uint64{},
				Pulled:     &fleet.SeriesPull{Series: 900000, Records: 900000},
				// The rotation, which the fixture did not carry, so the two cells
				// that read it were never rendered by anything. One of them told a
				// reader to grow a queue for a number that is mostly a branch more
				// room cannot change.
				Rotation: &fleet.Rotation{
					Completed: 2069, Truncated: 4, Offered: 100000, Queued: 90000,
					Deferred: 512, DeferredQueueFull: 12, DeferredNotBetter: 500,
					LastSeconds: 0.86,
				},
			},
			// The line that answers "what is affected". Its columns are
			// truncated and some of its objects name no strategy, because both
			// are true on the deployment this page is read on and both change
			// what the counts may be said to mean.
			Impact: fleet.Impact{
				Anomalies:   fleet.ColumnImpact{Objects: 86, Strategies: 70, Businesses: 9, Partial: true},
				Ours:        fleet.ColumnImpact{Objects: 5, Strategies: 4, Businesses: 2, Partial: true},
				Demoted:     fleet.ColumnImpact{Objects: 33, Strategies: 31, Businesses: 6},
				Undecidable: fleet.ColumnImpact{Objects: 12, Strategies: 12, Businesses: 3},
				ByDesign:    fleet.ColumnImpact{Objects: 4, Strategies: 4, Businesses: 1},
				// Fewer than 31 + 70: a strategy with objects in both columns is
				// one strategy, which is why this is a field and not a sum.
				Blind: fleet.ColumnImpact{Objects: 119, Strategies: 95, Businesses: 11, Partial: true},
				// By who acts: three parts, the middle one neither this
				// deployment's nor anybody else's yet.
				Alarmd:       fleet.ColumnImpact{Objects: 6, Strategies: 5, Businesses: 2, Partial: true},
				Undetermined: fleet.ColumnImpact{Objects: 14, Strategies: 13, Businesses: 4, Partial: true},
				Strategy:     fleet.ColumnImpact{Objects: 35, Strategies: 33, Businesses: 6, Partial: true},
				Data:         fleet.ColumnImpact{Objects: 40, Strategies: 30, Businesses: 5, Partial: true},
				NoStrategies: 7,
			},
		},
		"per_replica": replicas,
		"coverage": fleet.Disagreement{Comparable: true, HeldNotExpected: []string{"qg-blocked"},
			HeldNotExpectedTotal: 12},
		"page": map[string]int{"offset": 0, "limit": 50, "total": len(rows)},
	}
	// The operating judgment, decided by the same Go code the route uses,
	// from the census, capacity and records the fixture already carries --
	// so the four lines the page renders are checked against states that
	// cannot drift from what the server sends.
	health := fixture["health"].(fleet.HealthResponse)
	earlier := 0
	health.Schedule.OverdueAgo, health.Schedule.OverdueAgoSeconds = &earlier, 1800
	health.Load = fleet.LoadOf(&fleet.View{Schedule: health.Schedule, Capacity: health.Capacity,
		Demoted: demoted, GapSkips: retained.GapSkips, PerReplica: retained.PerReplica, Rebalance: rebalance}, at)
	fixture["health"] = health
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page.html"), page, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "smoke.js"), []byte(smokeHarness), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := exec.Command(node, filepath.Join(dir, "smoke.js"), dir).CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		t.Errorf("the page threw while rendering:\n%s", text)
		return
	}
	// The harness has to be able to fail, or a clean run means nothing. It
	// proves that by running one deliberately broken call and requiring it to
	// throw before reporting on the real ones.
	if !strings.Contains(text, "negative control threw") {
		t.Errorf("the harness did not prove it can fail; its clean result is worthless:\n%s", text)
	}
	// The one line on the page that answers "what is affected". A reader who
	// gets no answer here has to assemble it out of five object counts, which is
	// what they were doing.
	impactLine := lineStarting(text, "IMPACT ::")
	if impactLine == "" {
		t.Error("the impact line rendered nothing: the page answers how many objects and never " +
			"which alerts")
	}
	for _, want := range []string{
		// The union of the two columns. 31 + 70 is 101, and the fixture's union
		// is 95 because a strategy with objects in both is one strategy.
		"95 条策略拿不到检测结果",
		"11 个业务",
		// Whether to act, and by whom: three parts, the middle one neither
		// this deployment's nor anybody else's yet.
		"需要 alarmd 这边处理的：5 条策略（6 个对象）；待归因：13 条策略（14 个对象）——未确认属 alarmd，也不等于业务侧的，不要交出去；业务侧已确认：策略侧 33 条策略（35 个对象）、数据侧 30 条策略（40 个对象）。",
		// What the counts cannot cover. Both are true of a live deployment and
		// both change what the numbers may be taken to mean.
		"是下界",
		"没带策略信息",
	} {
		if !strings.Contains(impactLine, want) {
			t.Errorf("the impact line does not say %q:\n%s", want, impactLine)
		}
	}
	if strings.Contains(impactLine, "101 条策略") {
		t.Errorf("the impact line added the two columns instead of taking their union, which "+
			"overstates the number a reader acts on:\n%s", impactLine)
	}

	// The partition equation the page prints under the verdict. It is the only
	// statement on the page that says every object is accounted for, and a
	// column left out of it makes the sum quietly wrong while every cell above
	// still shows a plausible number.
	equation := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "SPLIT ") {
			equation = line
			break
		}
	}
	if equation == "" {
		t.Error("the page rendered no partition line; nothing on it says the columns account for " +
			"every object")
	} else if !strings.Contains(equation, "等于应有的") {
		t.Errorf("the partition line does not add up: %s\n(the fixture's columns sum to Covered, "+
			"so a page that drops one is the only way this fails)", equation)
	}

	// The first screen. Checks this reader acts on, in one sentence each with
	// the counts substituted; the ones already somebody else's under the fold.
	// The fixture has one stalled object, one that has missed a turn, two
	// refused queries (one in the cooldown pool, one not), a backend timing
	// out, a churning strategy, two skips (one current, one retained), and
	// five objects nobody can speak for: three the view holds undetermined and
	// two restored without their cause, under three folds with the stale
	// replica.
	todoLine := lineStarting(text, "CHECKS ::")
	for _, want := range []string{"1 个对象的轮次不再结束", "alarmd",
		// Two current: the object whose round ended skipping, and the one
		// losing rounds now from its record; the stopped one is in the
		// record section. The line says which part is still happening and
		// that none of it is a budget rejection -- so not capacity.
		"3 个对象跳过了检测，那段不补（最近 10 分钟内仍在跳过 1 个；1 个是滚动后的追赶（副本启动 5 分钟内），看这一组还有没有新增；跳过前最后一步：这一 Slot 没有尝试过，直接越过了重放范围 1 个、错过查询截止时间（重试到达时冻结的截止已过） 1 个；没有资源预算拒绝（只排除这一种拒绝，不排除别的容量或调度约束））",
		"恢复标准：10 分钟内没有新的跳过",
		"1 个对象到期没跑", "5 个对象现在说不出结论（3 种原因）",
		// Every line says where the evidence is, what to do next, and what
		// counts as recovered -- and "先等" says until when and whom after.
		"证据：展开这一行：最近一次激活失败原文", "下一步：先修激活",
		"恢复标准：执行版本追上目标发布（两者一致）且之后一轮激活成功",
		"下一步：等下一轮完成以补齐原因（行上有预计时刻；到点后仍可能在等待、重试或取消）；超过预计完成时间仍未补齐，它会落到\"到期没跑\"那一行（alarmd 的）",
		"恢复标准：10 分钟内没有新的跳过（\"仍在发生\"折归零）",
		// The two standings, first. The time is the viewer's clock and is not
		// asserted; everything after it is.
		"起没有生效：连续 120 轮激活失败（1 种原因），舰队在执行 bdc6ffcb 的内容，源已到 e7a1b2c3",
		"2 种副本级运行状态超出设计界，判定因此降级——策略配置刷新失败于 validate_catalog：plan retention 60h13m exceeds catalog retention 24h13m，新配置尚未发布，跑的是上一份好的目录",
		// The third standing: the numbers are the leader's round, the lag is
		// the replica table's, and the next step says what not to do first.
		"对象分布不均：abcde 持有 527 个（53.8%），fghij 持有 452 个，2 个就绪副本均分应是 489 个——持有多的那个副本上的跳过、超时、排队都是这个原因，不是容量——fghij 比 abcde 晚 1 小时 55 分 启动",
		"下一步：不要手动重启持有多的副本来均衡——重启只会把它的对象整批搬到剩下的副本，不会均分。已在移：本轮发出 9 个（每轮最多 9 个），最多与最少还差 75 个；看这一行的数在不在降；在均分回来之前",
		"恢复标准：Leader 的再平衡计划移动数归零（最多与最少之差回到均分的 5% 以内，这是调度器自己的容差）"} {
		if !strings.Contains(todoLine, want) {
			t.Errorf("the checks do not say %q:\n%s", want, todoLine)
		}
	}
	if !strings.HasPrefix(strings.TrimPrefix(todoLine, "CHECKS :: "), "控制面变更自 ") {
		t.Errorf("the fleet executing a stale publication is not the first line:\n%s", todoLine)
	}
	// Not confirmed as this deployment's is its own part, not on the list
	// above and not handed over: the bare refusal and the undecided windows.
	pendingLine := lineStarting(text, "PENDING ::")
	for _, want := range []string{"后端拒绝了 1 个对象的查询", "待确认", "个对象的窗口填不满，还分不出是谁的"} {
		if !strings.Contains(pendingLine, want) {
			t.Errorf("the undetermined part does not say %q:\n%s", want, pendingLine)
		}
	}
	if strings.Contains(todoLine, "待确认") || strings.Contains(todoLine, "后端拒绝了") {
		t.Errorf("an undetermined line is on the list of what is confirmed this deployment's:\n%s", todoLine)
	}
	// The record of past loss, apart from the lines above, with when it was
	// made; and the refusal that names a missing target is the strategy's,
	// in the governance fold, not 待确认 in the list above.
	for _, want := range []struct{ line, says string }{
		// The record holds only the stopped loss, says it stopped, and does
		// not call it "新增": the record keeps one skip per object.
		{"HISTORY ::", "1 个对象跳过了检测，那段不补（已停止，10 分钟以上没有再发生）——其中 1 个最近 1 小时内还发生过，最后一次 "},
		{"HISTORY ::", "下一步：已停止，不用让它停；那段永久没检测"},
		{"HISTORY ::", "恢复标准：记录不会归零；看的是同一对象有没有再跳过"},
		// A loss in progress on a ten-second object names its mechanism on
		// the row; the refused object's record carries its period too.
		// The mechanism is the last step before the skip, on the row, not
		// inferred from the period: this one retried after its deadline.
		{"SKIP qg-losing-now ::", "，10 秒周期。仍在发生（最近 10 分钟内跳过）跳过前最后一步：错过查询截止时间（重试到达时冻结的截止已过）"},
		{"SKIP qg-restart-catchup ::", "跳过前最后一步：这一 Slot 没有尝试过，直接越过了重放范围"},
		// The internal conflict beside the refusal, on the row and as a
		// second fact on the DEFECT line -- the refusal still has the object.
		{"INTERNAL qg-rejected ::", "内部错误：GAP_SCOPE_REASON_CONFLICT（input_a=QUERY_UNAVAILABLE input_b=QUERY_TIMEOUT），阶段 execute"},
		// The one shape every failure is read in, on rows of four different
		// shapes: the dependency from the error's words with the operation
		// and the last success; a timeout that names no dependency and says
		// so; a persisted skip as a confirmed loss; a refusal at the query
		// step from the backend that answered.
		{"BLOCKED qg-redis-commit ::", "卡在哪一步：环节待定位（Redis（控制面与状态存储），由错误原文判定，操作 commit）：类型待定位 error；影响：正在重试（这一轮没完成，会再跑）；最近一次成功 "},
		{"BLOCKED qg-one-clock ::", "卡在哪一步：数据查询（依赖待定位）：超时 QUERY_TIMEOUT；影响：结果待确认（这一轮结束了但结果不能采信）；本进程没见过它成功完成"},
		{"BLOCKED qg-losing-now ::", "卡在哪一步：调度接管（alarmd 自身（预算、截止、定义），由原因码判定）：容量不足 GAP_SKIPPED；影响：确认漏检（跳过记录已持久化，那段不补）"},
		{"BLOCKED qg-rejected ::", "卡在哪一步：数据查询（查询后端，由原因码判定）：被拒绝 "},
		{"CHECKS ::", "6 个对象命中程序缺陷（3 种），上报"},
		{"GOV ::", "2 个对象的查询被后端回\"表或字段不存在\""},
		{"PENDING ::", "证据：分组的后端回答只有状态码/状态词（非 200 的响应正文当前不保留，\"最近一次错误\"是结束这一轮的 alarmd 错误，不是后端原文）"},
		// The timeout and the short old-series window are not the data side's
		// until the query path and the fetch are ruled out: both are here, not
		// under governance.
		{"PENDING ::", "查询没有得到应答，3 个对象受影响（2 种症状，1 条策略）——客户端超时或 5xx，查询预算、网络、后端耗时哪一环还分不出"},
		{"PENDING ::", "下一步：先查查询链路：超时看查询预算、网络、后端耗时哪一环超了"},
		{"PENDING ::", "恢复所需的老序列数据不完整（1 条策略），恢复判不了——是数据没到还是 alarmd 没取到还分不出"},
		{"PENDING ::", "恢复标准：分出归属后转到对应行（策略侧或 alarmd）；不是等它消失"},
		{"HISTORY COUNT ::", "1 个对象，其中 1 个最近 1 小时内还发生过，最后一次 "},
		// The refusal line carries what its demoted object lost there, as the
		// refusal's consequence and not as capacity.
		{"GOV ::", "2 个对象的查询被后端回\"表或字段不存在\"（1 种回答，1 条策略，1 个业务）——按策略引用核，未逐个核过实际请求与元数据前不认定是策略写错；其中 1 个已降级，不再反复查；其中 1 个在被拒期间还跳过了检测（最近 10 分钟内 1 个）——冷却让旧轮次超出重放范围，首要原因是查询不可用，扩容无用"},
		{"GOV ::", "策略侧"},
		{"ACTION ::", "现在要做的：先修激活：看展开里最近一次失败文本与 activation_failed 日志；修好前所有策略变更都不生效（控制面变更自 "},
		{"ACTION ::", "；之后还有 8 类，按顺序在下面；待归因 5 类另看，别交出去"},
		// A record line's folds name what each loss is; the refusal's object
		// row says what it lost while under its line.
		{"GROUPS LOSS ::", "ONGOING（仍在发生（最近 10 分钟内跳过）） · 1 个对象 · 1 条策略 · 1 个业务"},
		{"GROUPS LOSS ::", "HISTORICAL（已停止（10 分钟以上没有再跳过）） · 1 个对象"},
		{"GROUPS LOSS ::", "AFTER_RESTART（滚动后的追赶（副本启动 5 分钟内跳过；每次滚动都有，通常几分钟内结束——是否结束看这一组还有没有新增）） · 1 个对象"},
		{"SKIP qg-restart-catchup ::", "，10 秒周期。滚动后的追赶（副本启动 5 分钟内跳过；每次滚动都有，通常几分钟内结束——是否结束看这一组还有没有新增）"},
		{"SKIP qg-demoted-rejected ::", "3 个 Slot，记录于 "},
		{"SKIP qg-demoted-rejected ::", "。在被拒期间跳过（冷却让旧轮次超出重放范围，首要原因是查询不可用）"},
		{"SKIP qg-losing-now ::", "。仍在发生（最近 10 分钟内跳过）"},
		// The operating judgment from the fixture's own census, capacity and
		// records: keeping up, no backlog, one loss in progress, and -- since
		// something is being lost -- the queued permits named as the
		// constraint; the limits name the half-hour trend and that nothing
		// estimates headroom.
		{"LOAD ::", "按时完成：跟得上，没有对象超期"},
		{"LOAD ::", "积压：没有，30 分 0 秒 前也没有"},
		{"LOAD ::", "漏检：正在发生——1 个对象最近 10 分钟内跳过了检测，另有 1 个是滚动后的追赶（副本启动 5 分钟内），看它还有没有新增、不由它问容量；另有 1 个被拒的对象在冷却期间跳过（首要原因是查询不可用，不是容量）"},
		// Behind (a loss in progress) while the leader's round would move
		// objects: the split is the constraint, named before the permits it
		// fills, and the sentence says what the build does about it.
		{"LOAD ::", "瓶颈：对象分布不均——abcde 持有 53.8% 的对象（527 / 均分 489），它的并发位子与队列满是因为它持有别的副本没持有的；加副本、加资源都分不走它的对象，已在移：本轮发出 9 个（每轮最多 9 个），最多与最少还差 75 个；看这一行的数在不在降"},
		// The other states the round can be in, each on the next-step sentence
		// the line prints: paused for the ready set to settle, with the time
		// left; a shadow round on a build that only plans; a round that
		// published nothing and says so; conflicts named beside the moves.
		{"SENTENCE rebalance-paused ::", "但这一轮没发：就绪副本集刚变过，等它稳定（还剩 23 秒）再发"},
		{"SENTENCE rebalance-shadow ::", "但这个构建只计划不执行——均分要等再平衡执行上线或下一次滚动"},
		{"SENTENCE rebalance-none ::", "这一轮一个都没发出去，3 个因指派记录同时被改本轮没发、下轮再算——若下一轮仍是 0"},
		{"SENTENCE rebalance-conflicts ::", "已在移：本轮发出 6 个（每轮最多 9 个），最多与最少还差 75 个，3 个因指派记录同时被改本轮没发、下轮再算；看这一行的数在不在降"},
		{"LOAD behind-permits ::", "瓶颈：查询并发位子——启动至今 56% 的取位子排过队，而工作在落后或在漏检"},
		// A budget rejection with the round beside it keeps its name and
		// says whose reading it is.
		{"LOAD budget-skewed ::", "瓶颈：内存派生的体量预算——已拒绝 41 次，这套资源装不下当前负载（这是唯一按定义就是容量的读数；其余读数为零只排除各自那一种约束）；对象分布不均（abcde 持有 53.8%），这个读数是它一个副本的，先看首屏\"对象分布不均\"那一行，不据此扩容"},
		{"LOAD ::", "限制条件：不推算还能承载多少对象——没有测这个，编出来的数会被当真；积压对照样本不足 1 小时（最年轻的副本索引还没跑满 1 小时）；资源占比是各进程启动至今的累计，不是最近 1 小时"},
		{"LOAD behind-permits ::", "按时完成：跟不上——7 个对象超期（最久 15 分 0 秒），1 小时按时率 88.9% 低于 6 小时 99.3%"},
		{"LOAD behind-permits ::", "积压：在涨——现在 7 个超期，1 小时 0 分 前 2 个"},
		{"LOAD unlocated ::", "按时完成：有 3 个对象超期但在追（最久 2 分 0 秒）——1 小时按时率 99.6% 不低于 6 小时 99.4%"},
		{"LOAD unlocated ::", "积压：现在 3 个超期；没有够早的对照样本，涨没涨说不出"},
		{"LOAD unlocated ::", "瓶颈：资源数字都不指向任何一处，但工作在落后或在漏检——约束在调度（重放边界、派发顺序），加资源不是这一步"},
		{"LOAD budget ::", "瓶颈：内存派生的体量预算——已拒绝 41 次，这套资源装不下当前负载（这是唯一按定义就是容量的读数；其余读数为零只排除各自那一种约束）"},
		{"LOAD nothing ::", "按时完成：没有副本带到期索引，说不出"},
		{"LOAD nothing ::", "瓶颈：没有副本报容量，判不了"},
		{"LOAD nothing ::", "没有到期索引，按时与积压两项判不了；没有容量数据，瓶颈一项判不了"},
		// A line whose objects are all in the pool says so, in the pool card's
		// words, so the two cannot read as different verdicts.
		{"ERR qg-stuck-slot ::", "最近一次错误：alarmd state: gap guard conflict: expected 41 got 43（*errors.errorString），Slot "},
		{"ERR qg-stuck-slot ::", "，同一 Slot 连续 3 次，"},
		{"SENTENCE demoted ::", "351 个对象的查询被后端回\"表或字段不存在\"（1 种回答，351 条策略，59 个业务）——按策略引用核，未逐个核过实际请求与元数据前不认定是策略写错；其中 351 个已降级，不再反复查"},
		{"SENTENCE budget ::", "5 个对象跳过了检测，那段不补（最近 10 分钟内仍在跳过 2 个；资源预算拒绝 3 个——只有这部分是资源装不下）"},
	} {
		if line := lineStarting(text, want.line); !strings.Contains(line, want.says) {
			t.Errorf("%s does not say %q:\n%s", want.line, want.says, line)
		}
	}
	if strings.Contains(todoLine, "策略引用了后端说不存在的表或字段") {
		t.Errorf("the refusal naming a missing target is still on the list this reader acts on:\n%s", todoLine)
	}
	// Opening a standing's line names its replicas, not objects.
	for _, want := range []struct{ line, says string }{
		{"GROUPS CUTOVER ::", "schedule_cutover/schedule_conflict · 副本 abcde，没有可列的对象"},
		// The guard-held fold names the trigger and that the window is still
		// short; the reader is not sent to edit a strategy.
		{"GROUPS WINDOW ::", "保护未解除（最初触发 CONFIG_DRIFT） · 1 个对象 · 1 条策略 · 1 个业务"},
		{"BASIS CUTOVER ::", "最近一次激活失败：alarmd controlplane: schedule activation conflict（副本 abcde）。伴随证据：segment_content_freshness_total{stale}"},
		{"GROUPS DEGRADED ::", "OPEN_ALERT_SET_STALE（已开告警集合的副本超过设计允许的时间没拿到消费者的发布，恢复门在用旧知识） · 副本 fghij，没有可列的对象"},
		{"GROUPS DEGRADED ::", "CONTROL_SOURCE_STALE（策略源超过设计允许的时间没有刷新成功，跑的是上一份好的目录——最近一次失败于 validate_catalog：plan retention 60h13m exceeds catalog retention 24h13m） · 副本 abcde，没有可列的对象"},
	} {
		if line := lineStarting(text, want.line); !strings.Contains(line, want.says) {
			t.Errorf("%s does not say %q:\n%s", want.line, want.says, line)
		}
	}
	for _, mustNot := range []string{"数据侧", "策略侧", "查询后端没有应答"} {
		if strings.Contains(todoLine, mustNot) {
			t.Errorf("the to-do checks say %q: that is somebody else's confirmed work, and it belongs "+
				"under the governance fold, not on the reader's list:\n%s", mustNot, todoLine)
		}
	}
	governance := lineStarting(text, "GOV ::")
	for _, want := range []string{"数据侧", "序列活不过检测窗口", "策略侧", "1 个对象持续没有数据"} {
		if !strings.Contains(governance, want) {
			t.Errorf("the governance fold does not say %q:\n%s", want, governance)
		}
	}
	brief := lineStarting(text, "BRIEF ::")
	for _, want := range []string{"执行情况：跟得上", "9000 轮里 99.6% 在下一轮到期前完成（6 小时 99.4%）",
		"被挡回 120 轮，其中 118 轮仍按时完成", "1900 个在等下次（33 个在冷却）", "12 个迟到未超一个周期", "1 个接管后还没跑第一轮",
		// From the server's arithmetic: lines with something on them now, and
		// distinct objects under them; the record apart. The fixture has 11
		// lines this reader acts on, 19 distinct objects under them now (16
		// rows plus the 3 the view holds undetermined; the two standings have
		// none), and one retained record made an hour ago.
		// Three parts from the server's arithmetic, then what is being lost
		// now and what the refused objects lost, apart from the record.
		"需要处理：alarmd 已确认 9 类（16 个对象，去重）；待归因 5 类（15 个对象）；业务侧已确认 3 类（4 个对象）在运营治理。正在漏检 1 个对象（最近 10 分钟内跳过，最近一次 ",
		"另有 1 个是滚动后的追赶漏检（副本启动 5 分钟内），看它还有没有新增",
		"被拒的对象里 1 个在冷却期间跳过了检测（最近 10 分钟内 1 个），首要原因是查询不可用；已停止的漏检记录 1 个对象另列",
		// On time, and on a stale publication: both true at once, and the
		// first sentence says both.
		"起没有生效：舰队在执行 bdc6ffcb 的内容，源已到 e7a1b2c3，连续 120 轮激活失败",
		// Eight objects carry no cause. This line said "全部对象都有结论" over a
		// grid showing them; completeness is about conclusions, and they have
		// none yet.
		// Two completenesses, said apart: every object's state, and whether
		// the detail behind the lines is whole.
		"状态覆盖：8 个对象没留下成因，还说不清归谁；各自再跑完一轮就补上；1 处覆盖缺口。明细不全：1 个副本的异常清单被截断，下面的分组数和策略数是样本"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief does not say %q:\n%s", want, brief)
		}
	}
	// What produced the numbers, before any of them is read.
	buildLine := lineStarting(text, "BUILD ::")
	if !strings.Contains(buildLine, "alarmd 0.2.4506（62ee924d），2 个副本一致") {
		t.Errorf("the build line does not name the one build both replicas run:\n%s", buildLine)
	}
	// Opening a check lists its folds with their counts; a fold with no objects
	// says so rather than offering an empty table.
	groups := lineStarting(text, "GROUPS ::")
	for _, want := range []string{"UNDETERMINED · 3 个对象", "RESTORED_WITHOUT_CAUSE · 2 个对象",
		"SNAPSHOT_STALE · 副本级缺口，没有可列的对象"} {
		if !strings.Contains(groups, want) {
			t.Errorf("the groups of OBSERVATION_GAP do not say %q:\n%s", want, groups)
		}
	}

	// The four dimensions on each row, as values. No sentence on the row
	// decides anything; the row says what the object is doing, how its last
	// round ended, since when, and what its windows hold.
	for _, want := range []struct{ object, now, result, window string }{
		{"qg-stalled", "轮次不结束", "完成 · COMPLETED_WITH_UNAVAILABLE", "—"},
		{"qg-two-clocks", "—", "完成 · QUERY_TIMEOUT", "—"},
		{"qg-cooldown", "冷却中", "失败 · QUERY_UNAVAILABLE", "—"},
		{"qg-rejected", "冷却中", "被拒绝 · QUERY_UNAVAILABLE", "—"},
		{"qg-blocked", "—", "失败 · source_blocked", "—"},
		{"qg-offhours", "生效时段外", "完成 · EFFECTIVE_TIME_INACTIVE", "—"},
		// The window's progress on the cell: not moving for twelve rounds, or
		// one more point than last round.
		{"qg-window-churn", "—", "完成 · HISTORY_WARMING", "9 个窗口 · 短 4 · 空 0 · 新 4 · 连续 40 轮 · 最差 2/9（上一轮 2，连续 12 轮没进展） · 不完整窗口上报了 3 个异常"},
		{"qg-window-stale-data", "—", "完成 · HISTORY_WARMING", "9 个窗口 · 短 4 · 空 0 · 新 0 · 连续 40 轮 · 最差 2/9（上一轮 1，在补）"},
		{"qg-window-starved", "—", "完成 · HISTORY_WARMING", "3 个窗口 · 短 2 · 空 2 · 新 0 · 连续 40 轮 · 最差 0/14 · 检测用不了 2 个：REQUIRED_VALUE_MISSING"},
		{"qg-no-data", "—", "无数据 · FULL_EMPTY_COMPLETED", "—"},
		{"qg-plain", "—", "完成 · COMPLETED_WITH_UNAVAILABLE", "—"},
		{"qg-guard-held", "—", "完成 · CONFIG_DRIFT（保护沿用，非本轮）", "3 个窗口 · 短 1 · 空 0 · 新 0 · 连续 29 轮 · 最差 5/9"},
		{"qg-stuck-slot", "— · 卡在 " + at.Add(-3*time.Minute).In(time.Local).Format("15:04:05") + " 这个 Slot，第 3 次失败", "失败 · error", "—"},
		{"qg-late", "迟到 12 秒", "完成 · HISTORY_WARMING", "—"},
		{"qg-missed-turn", "超期 4 分 0 秒", "完成 · QUERY_TIMEOUT", "—"},
		{"qg-never", "接管后未跑", "完成 · COMPLETED_WITH_UNAVAILABLE", "—"},
		{"qg-skipped-hour-ago", "—", "— · GAP_SKIPPED", "—"},
	} {
		line := lineStarting(text, "ROW "+want.object+" ::")
		if line == "" {
			t.Errorf("no row was rendered for %s", want.object)
			continue
		}
		cells := strings.Split(strings.TrimPrefix(line, "ROW "+want.object+" :: "), " | ")
		if len(cells) < 4 {
			t.Errorf("%s renders %d cells, want at least 4: %q", want.object, len(cells), line)
			continue
		}
		for index, part := range []string{want.now, want.result, "", want.window} {
			if part != "" && cells[index] != part {
				t.Errorf("%s cell %d = %q, want %q", want.object, index, cells[index], part)
			}
		}
	}
	// The third clock. An object saying its reason for less time than it has
	// been anomalous says both; one saying it since the start says the count.
	for _, want := range []struct{ object, since string }{
		{"qg-two-clocks", "1 小时 0 分 · 当前原因 12 分钟，连续 12 轮"},
		{"qg-one-clock", "1 小时 0 分 · 当前原因 连续 60 轮"},
		{"qg-plain", "1 小时 0 分"},
	} {
		line := lineStarting(text, "ROW "+want.object+" ::")
		cells := strings.Split(strings.TrimPrefix(line, "ROW "+want.object+" :: "), " | ")
		if len(cells) < 3 || cells[2] != want.since {
			t.Errorf("%s since cell = %q, want %q", want.object, line, want.since)
		}
	}
	// The waiting object names the moment it is next due; the wall-clock
	// rendering is the harness's locale, so only the shape is pinned.
	if line := lineStarting(text, "ROW qg-waiting ::"); !strings.Contains(line, "等下次 · 预计 ") {
		t.Errorf("qg-waiting renders %q, want it to say when it is next due", line)
	}
	// The retained skip carries its span in the evidence.
	if line := lineStarting(text, "SKIP qg-skipped-hour-ago ::"); !strings.Contains(line, "20 个 Slot") {
		t.Errorf("the retained skip renders %q, want the span with its Slot count", line)
	}

	// What the capacity panel says when a refresh arrives before any counter has
	// moved. Not throwing is checked by the harness; this checks it says the
	// right thing, because the branch that threw was the one meant to explain
	// exactly this state.
	capacity := lineStarting(text, "CAPACITY ::")
	if capacity == "" {
		t.Error("the capacity panel rendered nothing; the busiest computed panel on the page is " +
			"covered by nothing again")
	} else if !strings.Contains(capacity, "这两次读取之间没有新的拉取") {
		t.Errorf("after a refresh with the counters unmoved the capacity panel does not explain "+
			"why there is no average:\n%s", capacity)
	}

	// A link is rendered only when the environment said where its console is and
	// the reference carries everything that console needs. A wrong link sends a
	// reader to an error page and costs more than no link at all.
	for _, want := range []struct{ name, says, mustNotSay string }{
		{"configured", "https://monitor.example?bizId=7#/strategy-config/detail/1854", ""},
		{"nobiz", "(none)", "http"},
		{"unconfigured", "(none)", "http"},
	} {
		line := lineStarting(text, "LINK "+want.name+" ::")
		if line == "" {
			t.Errorf("strategyLink rendered nothing for the %s case", want.name)
			continue
		}
		if !strings.Contains(line, want.says) {
			t.Errorf("strategyLink %s renders %q, want %q", want.name, line, want.says)
		}
		if want.mustNotSay != "" && strings.Contains(line, want.mustNotSay) {
			t.Errorf("strategyLink %s renders %q, which is a link it cannot know is right",
				want.name, line)
		}
	}

	// A 24-hour window starts and ends at the same wall-clock time, and a
	// time-only label printed it as a range of zero length over a chart that
	// visibly covered a day.
	if line := lineStarting(text, "RANGE day ::"); line == "" {
		t.Error("rangeLabel rendered nothing for the 24-hour window")
	} else {
		ends := strings.SplitN(strings.TrimPrefix(line, "RANGE day :: "), " – ", 2)
		if len(ends) != 2 || ends[0] == ends[1] {
			t.Errorf("the 24-hour window renders %q: both ends read the same, so the label says "+
				"the window has no length", line)
		}
	}
	if line := lineStarting(text, "RANGE short ::"); line == "" {
		t.Error("rangeLabel rendered nothing for the short window")
	}

	// A filter is not a truncated snapshot. Filtering to one strategy -- the
	// ordinary way to use this page -- announced that the replica had failed to
	// publish its list, over an answer that was complete.
	for _, want := range []struct{ name, says, mustNotSay string }{
		{"filtered", "已按条件过滤掉 15 条", "没能发布完整清单"},
		{"truncated", "没能发布完整清单", "已按条件过滤"},
		{"plain", "", "条"},
	} {
		line := lineStarting(text, "TAIL "+want.name+" ::")
		if line == "" {
			t.Errorf("pageTail rendered nothing for the %s case", want.name)
			continue
		}
		if want.says != "" && !strings.Contains(line, want.says) {
			t.Errorf("pageTail %s renders %q, want it to say %q", want.name, line, want.says)
		}
		if strings.Contains(line, want.mustNotSay) {
			t.Errorf("pageTail %s renders %q, which says %q -- a different fact about the same "+
				"two numbers", want.name, line, want.mustNotSay)
		}
	}

	// Sentences that pair a number with a conclusion about that number. Every
	// one of these shipped to a live page stating something the same screen
	// contradicted, and none of them was executed by any check: the fixture
	// carried no value for the branch, so the branch never ran.
	for _, want := range []struct{ prefix, says, mustNotSay, because string }{
		{"POOL ::", "40 秒", "0 分钟",
			"过期不足一分钟时向下取整，印出的 0 分钟正是没有对象过期时的那句话"},
		{"POOL ::", "要超过这些对象自己的一个检测周期", "这个数远大于一个检测周期",
			"读数规则被当成对这个数的判定，40 秒的过期后面跟着出池路径有问题"},
		{"UNATTR gap ::", "本次判定不是停在这里", "本次判定就是停在这里",
			"有覆盖缺口时判定由缺口决定，这一栏却声称判定停在自己身上"},
		{"UNATTR gap ::", "已经计入", "",
			"这一栏和分栏等式在同一片网格里，等式只有六项而卡片有八张"},
		{"SPLIT", "横切计数", "",
			"横切的两张卡片不在等式里，页面要说出来，否则读的人会把八张卡片相加"},
		{"PARKED ::", "别相加", "",
			"此刻被拦下的数和六栏的最近一轮结果不是同一个时刻"},

		// The one loss with nothing on the page before this. It belongs to no
		// column, so it needed a line of its own or it could not be said at all.
		{"PRUNED ::", "从来没有被检测过", "",
			"这一段的时间点没有被评估过也不会补跑，页面上必须说得出来"},
		{"PRUNED ::", "1 小时 30 分", "",
			"最长的那一段要给出跨度，它是唯一能排序的量"},
		{"PRUNED ::", "无法得知", "",
			"跨度里有多少个时间点数不出来，给个数会被当成数出来的"},
		{"PRUNED ::", "上面每一栏都会这么报", "",
			"这些对象此刻正常，不说清就会被当成页面自相矛盾"},

		// The same panel in the states one fixture cannot be in at once. Each of
		// these is a branch that renders a sentence, and a branch that never ran
		// is a sentence nothing has read.
		{"VAR nogap unattr ::", "本次判定就是停在这里", "本次判定不是停在这里",
			"没有覆盖缺口时判定确实停在这一栏，两种情况必须分别说对"},
		{"VAR pool-stuck pool ::", "40 秒", "0 分钟",
			"这正是线上那一组数：进过很多、一个没出来、还在重试，而过期不足一分钟"},
		{"VAR pool-stuck pool ::", "一个都没出来过", "",
			"有成员无出口的池子要说出来，它的占用读起来和后端还没好一模一样"},
		{"VAR pool-dead pool ::", "既没出来过也没被重试过", "还在被重试",
			"没有重试的池子是出池路径停了，和后端没好是两回事"},
		{"VAR pool-empty pool ::", "本次进程还没有对象进过降级池", "一个都没出来过",
			"空池子不能和有成员的池子说同一句话"},
		{"VAR pool-exited pool ::", "最近一次出池", "",
			"出过池要给出最近一次的时间，否则只有计数无法判断出口是否还活着"},
		{"VAR overdue-present overdue ::", "最久的 900 秒", "",
			"这个量在这一栏一直是按秒印的，降级池那一栏却按分钟取整"},
		{"VAR overdue-truncated overdue ::", "不是 0，是不知道", "",
			"截断后的计数是下界，印成 0 会被读成没有对象漏掉"},
		{"VAR dispatch-off overdue ::", "结构上只可能是 0", "",
			"到期索引没启用时这里不能读成没有对象漏掉"},
		{"VAR expected-mismatch split ::", "对不上，先别信这几个数", "等于应有的",
			"分栏之和与应有对不上时必须自己说出来，这是读者唯一能发现分栏漏了一类的途径"},
		{"VAR healthy why ::", "证据齐全", "证据不全",
			"健康且没有覆盖缺口时不能还说证据不全"},
		{"VAR degraded why ::", "副本级运行状态超出设计界：控制面变更没有生效", "容量或架构",
			"副本级 standing 决定的 DEGRADED 要点名 standing，不能说成对象列表的容量或架构问题"},
		{"VAR degraded why ::", "（副本 abcde）；已开告警集合", "",
			"两种 standing 各说一次并带副本"},
		{"VAR degraded-objects why ::", "存在 alarmd 自己该负责的异常", "超出设计界",
			"没有 standing 时 DEGRADED 才是对象列表决定的"},
		// The first sentence of the brief, in the states the census has.
		{"VAR behind brief ::", "跟不上", "跟得上",
			"有超期且 1 小时按时率低于 6 小时时要说跟不上"},
		{"VAR behind brief ::", "7 个对象超期，最久 15 分 0 秒", "",
			"跟不上时要给出超期个数和最久多少"},
		{"VAR catching-up brief ::", "有超期但在追", "跟不上",
			"有超期但短窗按时率不低于长窗时不是跟不上"},
		{"VAR quiet-hour brief ::", "过去 1 小时没有轮次返回", "%",
			"没有轮次返回时不能印一个比率"},
		{"VAR no-census brief ::", "说不出是否按时", "跟得上",
			"没有普查时不能说按时"},
		// The queue verdict on the capacity panel, read from the same census.
		// The wait share alone said "支持扩容" on a live deployment with three
		// quarters of its CPU idle; whether queueing costs anything is a
		// question about deadlines, and only the census answers it.
		{"VAR behind cap ::", "而且在耽误到期任务（7 个超期", "不构成扩容理由",
			"排队且跟不上时才是位子成了约束"},
		{"VAR behind cap ::", "位子是约束，这一项支持扩容", "",
			"跟不上时要把结论说出来，不能让运维自己算"},
		{"VAR catching-up cap ::", "有 2 个超期但在追", "支持扩容",
			"在追上时先看能不能自己追上，不能直接建议扩容"},
		{"VAR no-census cap ::", "本构建没有普查，判不了", "支持扩容",
			"没有普查时排队占比不能单独变成扩容建议"},
		{"VAR healthy cap ::", "没有对象超期", "支持扩容",
			"没有超期时排队没耽误到期任务，不构成扩容理由"},
		// Which build the numbers came from, in the three shapes it has.
		{"VAR mixed-builds build ::", "2 个构建同时在跑——0.2.4506（62ee924d） 1 副本、0.2.4505（4f338bf3） 1 副本", "副本一致",
			"两个构建同时在跑时不能说一致，总数是混出来的"},
		{"VAR unreported-build build ::", "未上报版本（旧构建） 1 副本", "",
			"没上报版本的副本要按旧构建列出，不能归到某个版本下"},
		{"VAR no-builds build ::", "没有副本被计入", "副本一致",
			"没有副本被计入时说不出在跑什么"},

		// 被挡回 by cause. The old wording named one remedy -- grow the queue --
		// for a number that is mostly the branch more room cannot change.
		{"ROT only-not-better ::", "扩队列不会改变这个数", "扩队列有用",
			"全是排序结果时不能让人去扩队列"},
		{"QUEUE quiet ::", "此刻没有对象超期、没有正在发生的漏检——排队没耽误到期任务，不构成扩队列的理由", "扩队列有用",
			"没超期没漏检时队列满不是扩队列的理由"},
		{"QUEUE losing ::", "正在漏检 3 个对象：先看等待时长与消费速度", "扩队列有用",
			"正在漏检时先看等待与消费速度，不承诺扩队列有效"},
		{"QUEUE behind ::", "此刻 7 个对象超期：先看等待时长与消费速度——排队在增长、按时率在降（1 小时 88.9% 对 6 小时 99.3%）才是位子不够；只扩队列可能只是让任务等得更久，不保证有效", "扩队列有用",
			"跟不上时给出判据与两个按时率，不承诺扩队列有效"},
		{"QUEUE no-census ::", "本构建没有普查，判不了——别只凭这个数扩队列", "扩队列有用",
			"没普查时判不了"},
		{"ROT only-queue-full ::", "先看等待时长与消费速度", "扩队列有用",
			"全是没位置时扩队列确实有用，而且轮转会就地停下"},
		{"ROT mixed ::", "就绪队列没位置 12 次", "",
			"两种都有时先给出该看的那个数，不是只给总数"},
		{"ROT unsplit ::", "副本没有报告是哪一种原因", "扩队列有用",
			"副本没报告成因时说不出该不该处理，猜一个比不说更糟"},

		// Whether there are enough places, answered with an instrument that can
		// see the answer. The two gauges cannot: a caller queueing behind a full
		// budget begins and ends between two reads.
		{"PERMIT mostly-waited ::", "56%", "没有查询在排队",
			"多数查询等过位子时不能因为此刻队列空就说没有排队"},
		// The fixture's census has nothing overdue, so the share is queueing
		// and not a reason to add pods -- said as such, with the rates.
		{"PERMIT mostly-waited ::", "但没有对象超期（按时率 1 小时 99.6%、6 小时 99.4%），排队没耽误到期任务，不构成扩容理由", "支持扩容",
			"占比过半只说明有排队；有没有耽误到期任务由普查回答，没超期就不能建议扩容"},
		{"PERMIT never-waited ::", "没有一次排过队", "位子是约束",
			"一次都没等过才是真的有余量，这一支要和上一支说相反的话"},
		{"PERMIT nothing-asked ::", "无从谈起", "不是约束",
			"零次取位子里零次等待不是有余量，是什么都没量到"},
	} {
		line := lineStarting(text, want.prefix)
		if line == "" {
			t.Errorf("no %s line was rendered, so the check on its wording never ran", want.prefix)
			continue
		}
		if !strings.Contains(line, want.says) {
			t.Errorf("%s renders %q, want it to say %q -- %s", want.prefix, line, want.says, want.because)
		}
		if want.mustNotSay != "" && strings.Contains(line, want.mustNotSay) {
			t.Errorf("%s renders %q, which says %q -- %s", want.prefix, line, want.mustNotSay, want.because)
		}
	}
}

// lineStarting returns the harness line with this prefix, or "" if the render
// emitted none -- which is itself a result, and a different one from a line
// that came out empty.
func lineStarting(text, prefix string) string {
	for _, candidate := range strings.Split(text, "\n") {
		if strings.HasPrefix(candidate, prefix) {
			return candidate
		}
	}
	return ""
}

// smokeHarness stubs just enough DOM for the render functions and calls them.
// It is deliberately small: a fuller emulator would be a second implementation
// to maintain, and the failure being caught here needs nothing more than a real
// call stack.
// It also runs the one rule the page deliberately duplicates -- "can this
// window ever fill" -- against the verdicts the Go side computed, so the two
// copies are checked by execution rather than by a substring.
const smokeHarness = `
const fs = require('fs'), vm = require('vm'), path = require('path');
const dir = process.argv[2];
const html = fs.readFileSync(path.join(dir, 'page.html'), 'utf8');
const data = JSON.parse(fs.readFileSync(path.join(dir, 'fixture.json'), 'utf8'));
const script = html.slice(html.indexOf('<script>') + 8, html.lastIndexOf('</script>'));

function el(tag) {
  const n = {tag, className: '', title: '', value: '', hidden: false, style: {}, children: [], _t: ''};
  Object.defineProperty(n, 'textContent', {get() { return n._t; }, set(v) { n._t = String(v); n.children = []; }});
  n.appendChild = c => { n.children.push(c); return c; };
  n.addEventListener = () => {}; n.setAttribute = () => {}; n.removeAttribute = () => {}; n.remove = () => {};
  n.querySelector = () => el('div'); n.querySelectorAll = () => [];
  n.classList = {add(){}, remove(){}, toggle(){}, contains(){return false;}};
  n.focus = () => {}; n.click = () => {};
  return n;
}
// The full rendered text of a node, children included. A node's textContent
// here is only what was assigned to it directly, and every line the impact
// block builds is assembled out of appended children.
function textOf(node) {
  if (!node) { return ''; }
  return (node._t || '') + (node.children || []).map(textOf).join('');
}
const store = {};
const document = {getElementById: id => store[id] || (store[id] = el('div')), createElement: el,
  createTextNode: t => { const n = el('#text'); n.textContent = t; return n; },
  createElementNS: () => el('svg'), querySelector: () => el('div'), querySelectorAll: () => [],
  addEventListener: () => {}, body: el('body'), documentElement: el('html'), readyState: 'complete'};
// A clock the harness advances on purpose.
//
// Every cumulative figure in the capacity panel becomes a rate from two reads,
// and which branch runs depends entirely on how far apart those reads are:
// zero apart gives no rate at all, far enough apart with the counter unmoved
// gives a rate of zero. Those are different code paths and only the second one
// has ever thrown. Left on the real clock, three renders inside one millisecond
// take the first path and the check passes without executing the branch it was
// written for -- which is what happened when this was first written.
let clockMs = Date.UTC(2026, 8, 14, 10, 0, 0);
class FakeDate extends Date {
  constructor(...args) { if (args.length === 0) { super(clockMs); } else { super(...args); } }
  static now() { return clockMs; }
}
const ctx = {document, console, JSON, Date: FakeDate, Math, Object, Array, String, Number, Boolean, RegExp,
  Error, Promise, isFinite, isNaN, parseInt, parseFloat, encodeURIComponent, decodeURIComponent,
  setInterval: () => 0, clearInterval: () => {}, setTimeout: () => 0, clearTimeout: () => {},
  fetch: () => new Promise(() => {}),
  location: {href: 'http://x/alarmd/', search: '', pathname: '/alarmd/', origin: 'http://x'},
  localStorage: {getItem: () => null, setItem: () => {}, removeItem: () => {}},
  history: {replaceState(){}, pushState(){}}, URL, URLSearchParams, navigator: {userAgent: 'node'}};
ctx.window = ctx; ctx.globalThis = ctx; ctx.self = ctx;
vm.createContext(ctx);

// The whole script, including its top-level wiring. Nothing is stripped: a
// filter that drops lines cannot see that a statement spans several of them,
// and stripping the first line of one leaves its closing brace behind.
try { vm.runInContext(script, ctx, {filename: 'page.js'}); }
catch (e) { console.error('the page did not even load: ' + e.constructor.name + ': ' + e.message); process.exit(1); }

// Prove the harness can fail before trusting that it did not.
try {
  vm.runInContext('objectRow(undefinedRowVariable)', ctx);
  console.error('negative control did not throw; a clean result here would mean nothing');
  process.exit(1);
} catch (e) { console.log('negative control threw ' + e.constructor.name); }

const calls = [
  ['objectRow (every row shape)', () => data.anomalies.forEach(r => ctx.objectRow(r))],
  ['renderDeployment', () => ctx.renderDeployment(data.health)],
  ['renderChecks', () => { ctx.latestTodo = data.todo; ctx.renderChecks(data.checks); }],
  ['renderReplicas', () => ctx.renderReplicas(data.per_replica)],
  ['renderCoverage', () => ctx.renderCoverage(data.coverage)],
];
let failed = 0;
for (const [name, fn] of calls) {
  try { fn(); } catch (e) { failed++; console.error(name + ': ' + e.constructor.name + ': ' + e.message); }
}

// The page's own copy of the rule, run against the verdicts Go computed. A
// field renamed on one side makes the page read undefined, the comparison
// false, and every permanently short window render as "still filling" -- with
// no error anywhere. Only running both can see it.
console.log('SPLIT ' + (store['splitBasis'] ? store['splitBasis'].textContent : '(not rendered)'));

// Three sentences a reader acts on that no check executed. Each of them is
// assembled from a count plus a conclusion about that count, and each of them
// shipped saying something the same screen contradicted.
console.log('POOL :: ' + (store['poolFlowHint'] ? store['poolFlowHint'].textContent : '(not rendered)'));
console.log('UNATTR gap :: ' + (store['unattributedHint'] ? store['unattributedHint'].textContent : '(not rendered)'));
console.log('WHY gap :: ' + (store['why'] ? store['why'].textContent : '(not rendered)'));
console.log('PARKED :: ' + (store['overdueHint'] ? store['overdueHint'].textContent : '(not rendered)'));
console.log('PRUNED :: ' + (store['prunedSkips'] ? store['prunedSkips'].textContent : '(not rendered)'));

// The first screen and the fold under it, as rendered.
console.log('CHECKS :: ' + textOf(store['checkRows']));
console.log('PENDING :: ' + textOf(store['pendingRows']));
console.log('HISTORY :: ' + textOf(store['historyRows']));
console.log('HISTORY COUNT :: ' + textOf(store['historyCount']));
console.log('GOV :: ' + textOf(store['govRows']));
console.log('ACTION :: ' + textOf(store['briefAction']));
// The pool suffix, on a line shaped like the live one: every object demoted.
console.log('SENTENCE demoted :: ' + ctx.checkSentence({code: 'QUERY_TARGET_MISSING', objects: 351, current: 351, demoted: 351,
  strategies: 351, businesses: 59, groups: [{key: 'response=status_space_table_id_field_is_not_exists', objects: 351}]}, 351));
// A record line over a budget rejection: the one fold the word capacity is
// earned for, said as such and only for that part.
console.log('SENTENCE budget :: ' + ctx.checkSentence({code: 'DETECTION_ABANDONED', objects: 5, current: 5, group_by: 'loss',
  groups: [{key: 'EXECUTION_BUDGET_EXHAUSTED', objects: 3}, {key: 'ONGOING', objects: 2}]}, 5));
console.log('BRIEF :: ' + ['briefSchedule', 'briefTodo', 'briefBlind'].map(id => textOf(store[id])).join(' | '));
// The split line's next step in every state the leader's round can be in.
const round = {ready_workers: 2, assigned: 979, target: 489, most_owned: 527, least_owned: 452, batch: 9, planned_moves: 9, stop_spread_percent: 5,
  most_owned_by: 'bk-monitor-alarmd-trigger-5bdb679ddf-abcde', least_owned_by: 'bk-monitor-alarmd-trigger-5bdb679ddf-fghij'};
for (const [name, extra] of Object.entries({
  'paused': {paused: true, paused_for_seconds: 22.4},
  'shadow': {shadow: true},
  'none': {published_moves: 0, conflicts: 3},
  'conflicts': {published_moves: 6, conflicts: 3},
})) {
  console.log('SENTENCE rebalance-' + name + ' :: ' + ctx.nextWords('OWNERSHIP_SKEWED', {rebalance: Object.assign({}, round, extra)}));
}
console.log('BUILD :: ' + textOf(store['buildLine']));
// The operating judgment at the top of the capacity panel, with its limits.
console.log('LOAD :: ' + textOf(store['loadLines']) + ' ｜ ' + textOf(store['loadLimits']));
// The same judgment in the states a deployment is actually in: behind on
// permits, behind with nothing pointing anywhere, a budget rejection while
// keeping up, and nothing to read from.
const loadStates = {
  'behind-permits': {on_time: {state: 'FALLING_BEHIND', overdue: 7, oldest_late_seconds: 900, rate_1h: 88.9, rate_6h: 99.3},
    backlog: {state: 'GROWING', now: 7, earlier: 2, span_seconds: 3600},
    loss: {state: 'NONE', ongoing: 0, while_demoted_recent: 0, window_seconds: 600},
    bottleneck: {resource: 'PERMITS', budget_rejections: 0, memory_limit_hits: 0, throttled_share: 0.01, permit_wait_share: 0.56, queue_full: 12},
    limits: ['NO_HEADROOM_ESTIMATE', 'COUNTERS_SINCE_START']},
  'unlocated': {on_time: {state: 'CATCHING_UP', overdue: 3, oldest_late_seconds: 120, rate_1h: 99.6, rate_6h: 99.4},
    backlog: {state: 'UNKNOWN', now: 3},
    loss: {state: 'IN_PROGRESS', ongoing: 2, while_demoted_recent: 5, window_seconds: 600},
    bottleneck: {resource: 'UNLOCATED', budget_rejections: 0, memory_limit_hits: 0, permit_wait_share: 0.1, queue_full: 0},
    limits: ['NO_HEADROOM_ESTIMATE', 'COUNTERS_SINCE_START']},
  'budget': {on_time: {state: 'KEEPING_UP', overdue: 0, oldest_late_seconds: 0, rate_1h: 99.6, rate_6h: 99.4},
    backlog: {state: 'NONE', now: 0, earlier: 0, span_seconds: 3600},
    loss: {state: 'NONE', ongoing: 0, while_demoted_recent: 0, window_seconds: 600},
    bottleneck: {resource: 'BUDGET', budget_rejections: 41, memory_limit_hits: 0, queue_full: 0},
    limits: ['NO_HEADROOM_ESTIMATE', 'COUNTERS_SINCE_START']},
  'budget-skewed': {on_time: {state: 'KEEPING_UP', overdue: 0, oldest_late_seconds: 0, rate_1h: 99.6, rate_6h: 99.4},
    backlog: {state: 'NONE', now: 0, earlier: 0, span_seconds: 3600},
    loss: {state: 'NONE', ongoing: 0, while_demoted_recent: 0, window_seconds: 600},
    bottleneck: {resource: 'BUDGET', budget_rejections: 41, memory_limit_hits: 0, queue_full: 0,
      skew: {assigned: 979, target: 489, most_owned: 527, least_owned: 452, most_owned_by: 'bk-monitor-alarmd-trigger-5bdb679ddf-abcde',
        least_owned_by: 'bk-monitor-alarmd-trigger-5bdb679ddf-fghij', batch: 9, planned_moves: 9, stop_spread_percent: 5, shadow: true}},
    limits: ['NO_HEADROOM_ESTIMATE', 'COUNTERS_SINCE_START']},
  'nothing': {on_time: {state: 'UNKNOWN'}, backlog: {state: 'UNKNOWN', now: 0}, loss: {state: 'NONE', window_seconds: 600},
    bottleneck: {resource: 'UNKNOWN'}, limits: ['NO_HEADROOM_ESTIMATE', 'NO_CENSUS', 'NO_CAPACITY']},
};
for (const [name, load] of Object.entries(loadStates)) {
  try { ctx.renderLoad(load); }
  catch (e) { console.error('renderLoad (' + name + '): ' + e.message); failed++; continue; }
  console.log('LOAD ' + name + ' :: ' + textOf(store['loadLines']) + ' ｜ ' + textOf(store['loadLimits']));
}
// Opening a line renders its folds.
ctx.openCheck = 'OBSERVATION_GAP';
ctx.renderChecks(data.checks);
console.log('GROUPS :: ' + textOf(store['groups']));
ctx.openCheck = 'WINDOW_UNDECIDED';
ctx.renderChecks(data.checks);
console.log('GROUPS WINDOW :: ' + textOf(store['groups']));
ctx.openCheck = 'CUTOVER_FAILING';
ctx.renderChecks(data.checks);
console.log('GROUPS CUTOVER :: ' + textOf(store['groups']));
console.log('BASIS CUTOVER :: ' + textOf(store['detailBasis']));
ctx.openCheck = 'REPLICA_DEGRADED';
ctx.renderChecks(data.checks);
console.log('GROUPS DEGRADED :: ' + textOf(store['groups']));
// The record line's folds name what each loss is, and the refusal line
// carries what its demoted objects lost there.
ctx.openCheck = 'DETECTION_ABANDONED';
ctx.renderChecks(data.checks);
console.log('GROUPS LOSS :: ' + textOf(store['groups']));
ctx.openCheck = '';

// The four dimensions each row shows, read off the rendered cells.
for (const row of data.anomalies) {
  let tr;
  try { tr = ctx.objectRow(row); }
  catch (e) { console.error('objectRow threw on ' + row.query_group + ': ' + e.message); failed++; continue; }
  const cells = tr.children.map(textOf);
  console.log('ROW ' + row.query_group + ' :: ' + cells.slice(1, 5).join(' | '));
  if (row.skip) { console.log('SKIP ' + row.query_group + ' :: ' + textOf(tr.children[tr.children.length - 1])); }
  if (row.last_error) { console.log('ERR ' + row.query_group + ' :: ' + textOf(tr.children[tr.children.length - 1])); }
  if (row.internal_failure) { console.log('INTERNAL ' + row.query_group + ' :: ' + textOf(tr.children[tr.children.length - 1])); }
  if (row.blocked) { console.log('BLOCKED ' + row.query_group + ' :: ' + textOf(tr.children[tr.children.length - 1])); }
}

// The capacity panel on a refresh that arrives after a real interval with the
// counters unmoved -- which is what "clicked refresh too fast" produces once
// the page has been open a while: the interval is real, nothing was pulled in
// it, and the rate is zero rather than absent.
//
// renderDeployment above already took the first read. The clock is advanced so
// the second one has an interval to divide by; without that the rate is absent
// instead of zero and the branch that threw never runs.
clockMs += 30000;
try { ctx.renderCapacity(data.health.capacity, [], data.health.schedule); }
catch (e) { console.error('renderCapacity (refresh, counters unmoved): ' + e.constructor.name + ': ' + e.message); failed++; }
console.log('CAPACITY :: ' + textOf(store['capCards']));

// The same panel over the rotation shapes a deployment is actually in.
//
// 被挡回 is one number produced by two branches with opposite answers: a ready
// queue with no room, which more room fixes, and a recovery queue whose objects
// are all due sooner, which the dispatcher itself calls a decision rather than a
// lack of room. Which one dominates decides whether there is anything to do, so
// each shape has to be rendered and read.
const permitShapes = {
  'mostly-waited': {permit_acquires: 40000, permit_waits: 22400, waiting: 0},
  'never-waited': {permit_acquires: 40000, permit_waits: 0, waiting: 0},
  // A replica that has not run a query yet. Zero waits out of zero asks is not
  // "there is room", it is nothing measured.
  'nothing-asked': {permit_acquires: 0, permit_waits: 0, waiting: 0},
};
for (const [name, override] of Object.entries(permitShapes)) {
  store['capCards'].textContent = '';
  try { ctx.renderCapacity(Object.assign({}, data.health.capacity, override), [], data.health.schedule); }
  catch (e) { console.error('renderCapacity (' + name + '): ' + e.message); failed++; continue; }
  console.log('PERMIT ' + name + ' :: ' + textOf(store['capCards']));
}

const rotations = {
  'mixed': {deferred: 512, deferred_queue_full: 12, deferred_not_better: 500},
  'only-not-better': {deferred: 500, deferred_queue_full: 0, deferred_not_better: 500},
  'only-queue-full': {deferred: 12, deferred_queue_full: 12, deferred_not_better: 0},
  // A replica that has not been upgraded past the split. Saying nothing about
  // the cause is right; saying the old sentence would be a guess.
  'unsplit': {deferred: 512, deferred_queue_full: 0, deferred_not_better: 0},
};
for (const [name, rotation] of Object.entries(rotations)) {
  store['capCards'].textContent = '';
  const capacity = Object.assign({}, data.health.capacity,
    {rotation: Object.assign({}, data.health.capacity.rotation, rotation)});
  try { ctx.renderCapacity(capacity, [], data.health.schedule); }
  catch (e) { console.error('renderCapacity (' + name + '): ' + e.message); failed++; continue; }
  console.log('ROT ' + name + ' :: ' + textOf(store['capCards']));
}
// A full ready queue is judged on whether due work is finishing and whether
// detection is being lost, not promised a remedy: the same full queue over
// a deployment with nothing overdue and nothing being lost, over one losing
// rounds now, and over one falling behind.
const savedTodo = ctx.latestTodo;
const fullQueue = Object.assign({}, data.health.capacity,
  {rotation: Object.assign({}, data.health.capacity.rotation, rotations['only-queue-full'])});
const queueStates = {
  'quiet': {todo: Object.assign({}, data.todo, {ongoing: 0}), schedule: data.health.schedule},
  'losing': {todo: Object.assign({}, data.todo, {ongoing: 3}), schedule: data.health.schedule},
  'behind': {todo: Object.assign({}, data.todo, {ongoing: 0}),
             schedule: Object.assign({}, data.health.schedule, {overdue: 7, on_time_1h: 8000, on_time_6h: 53640})},
  'no-census': {todo: Object.assign({}, data.todo, {ongoing: 0}), schedule: null},
};
for (const [name, state] of Object.entries(queueStates)) {
  store['capCards'].textContent = '';
  ctx.latestTodo = state.todo;
  try { ctx.renderCapacity(fullQueue, [], state.schedule); }
  catch (e) { console.error('renderCapacity (queue ' + name + '): ' + e.message); failed++; continue; }
  console.log('QUEUE ' + name + ' :: ' + textOf(store['capCards']));
}
ctx.latestTodo = savedTodo;

// The impact line -- the only thing on the page that answers "what is affected"
// rather than "how many objects".
console.log('IMPACT :: ' + textOf(store['impact']));

// The same panel in the other states a deployment is actually in.
//
// One fixture renders one branch of each sentence here, and every sentence on
// this panel is a count plus a conclusion about that count. Four of them
// shipped to a live page saying something that page contradicted, and all four
// were unexecuted for the same reason: the single fixture left the field at
// zero, so the branch never ran and nothing ever read the wording.
//
// The overrides are JSON field names on purpose. A Go-side rename that the page
// does not follow shows up here as a branch that stops rendering.
const variants = {
  // Nothing took the verdict first, so the unattributed cell is the answer.
  'nogap': {gaps: []},
  // The live state this panel exists for: a pool with members, no exits, and
  // retries still happening. The fixture's own pool has exits, so this branch
  // had never run.
  // No exits clears the exit time with it. The two are written together, so a
  // variant that zeroed one and kept the other would describe a deployment the
  // tracker cannot produce -- and a check run against an impossible state
  // proves nothing about the page.
  'pool-stuck': {demotion_entries: 309, demotion_exits: 0, demotion_extensions: 40,
                 demoted_due: 6, demoted_due_oldest_seconds: 40, last_demotion_exit: null},
  // Members, no exits, and nothing retrying either -- the way out has stopped.
  'pool-dead': {demotion_entries: 309, demotion_exits: 0, demotion_extensions: 0,
                demoted_due: 0, last_demotion_exit: null},
  'pool-empty': {demotion_entries: 0, demotion_exits: 0, demoted_due: 0, last_demotion_exit: null},
  'pool-exited': {demotion_entries: 40, demotion_exits: 7, demoted_due: 0},
  // An overdue that is minutes old rather than seconds, so the two renderers of
  // this same quantity can be compared.
  'overdue-present': {overdue: {total: 3, oldest_seconds: 900}},
  'overdue-truncated': {overdue: {total: 0, truncated: true}},
  // No dispatch suppression at all: a zero that cannot mean anything yet.
  'dispatch-off': {dispatch: null},
  // A denominator that disagrees with the columns. The page has a sentence for
  // this and nothing had ever produced it.
  'expected-mismatch': {expected: 2075},
  'healthy': {health: 'HEALTHY', gaps: [], unattributed: 0},
  'degraded': {health: 'DEGRADED', gaps: [], unattributed: 0},
  'degraded-objects': {health: 'DEGRADED', gaps: [], unattributed: 0, degradations: [], activation: null},
  // The census in the other states it has: falling behind (overdue, and the
  // hour's rate below the six hours'), overdue but the rate not falling, a
  // quiet hour with nothing returned, and no census at all.
  'behind': {schedule: {waiting: 1800, late: 40, overdue: 7, never: 0, oldest_late_seconds: 900,
                        completed_1h: 8000, on_time_1h: 7000, completed_6h: 54000, on_time_6h: 53000}},
  'catching-up': {schedule: {waiting: 1800, late: 40, overdue: 2, never: 0, oldest_late_seconds: 130,
                             completed_1h: 8000, on_time_1h: 7950, completed_6h: 54000, on_time_6h: 53000}},
  'quiet-hour': {schedule: {waiting: 1900, late: 0, overdue: 0, never: 0,
                            completed_1h: 0, on_time_1h: 0, completed_6h: 54000, on_time_6h: 53700}},
  'no-census': {schedule: null},
  // The build line in its other shapes: a rollout with one replica on each
  // build, a replica on a build that reports none, and nothing counted.
  'mixed-builds': {builds: [
    {build: {version: '0.2.4506', commit: '62ee924d00000000', schema_version: 'v3'}, replicas: ['pod-a']},
    {build: {version: '0.2.4505', commit: '4f338bf300000000', schema_version: 'v3'}, replicas: ['pod-b']}]},
  'unreported-build': {builds: [
    {build: {version: '0.2.4506', commit: '62ee924d00000000', schema_version: 'v3'}, replicas: ['pod-a']},
    {build: {version: '', commit: '', schema_version: ''}, replicas: ['pod-b']}]},
  'no-builds': {builds: [], per_replica: []},
};
const variantCells = ['why', 'poolFlowHint', 'unattributedHint', 'splitBasis', 'overdueHint', 'briefSchedule',
                      'capCards', 'buildLine'];
for (const [name, override] of Object.entries(variants)) {
  // Cleared first. A cell whose branch does not run this time keeps whatever
  // the previous render wrote, and the emitted line would then report the
  // previous variant's wording as this one's -- the check reading a stale value
  // and calling it a result.
  for (const id of variantCells) { document.getElementById(id).textContent = ''; }
  let view;
  try {
    view = Object.assign({}, data.health, override);
    ctx.renderDeployment(view);
  } catch (e) {
    console.error('renderDeployment (' + name + '): ' + e.constructor.name + ': ' + e.message);
    failed++;
    continue;
  }
  for (const [label, id] of [['why', 'why'], ['pool', 'poolFlowHint'],
                             ['unattr', 'unattributedHint'], ['split', 'splitBasis'],
                             ['overdue', 'overdueHint'], ['brief', 'briefSchedule'],
                             ['cap', 'capCards'], ['build', 'buildLine']]) {
    // Children included: the capacity panel is built from appended cards, and
    // its own textContent is empty however much it rendered.
    const said = textOf(store[id]);
    console.log('VAR ' + name + ' ' + label + ' :: ' + (said || '(not rendered)'));
  }
}

// The strategy link, in the three states it has: configured and complete,
// configured but the reference carries no business id, and not configured at
// all. A wrong link is worse than none, so two of the three must render none.
for (const c of data.strategy_link_cases || []) {
  ctx.consoleBase = c.base;
  let href;
  try { href = ctx.strategyLink(c.strategy); }
  catch (e) { console.error('strategyLink threw on ' + c.name + ': ' + e.message); failed++; continue; }
  console.log('LINK ' + c.name + ' :: ' + (href || '(none)'));
}
ctx.consoleBase = '';

// The window label, on a range whose two ends are the same wall-clock time on
// two different days -- which is every 24-hour window, and rendered as a range
// of zero length.
for (const c of data.range_cases || []) {
  let label;
  try { label = ctx.rangeLabel(c.start, c.end); }
  catch (e) { console.error('rangeLabel threw on ' + c.name + ': ' + e.message); failed++; continue; }
  console.log('RANGE ' + c.name + ' :: ' + label);
}

// The count line, on the three states it has to tell apart. A filter narrowing
// the list is not a replica failing to publish it, and this used to report the
// second whenever the first happened.
for (const c of data.page_tail_cases || []) {
  let tail;
  try { tail = ctx.pageTail(c.objects, c.total); }
  catch (e) { console.error('pageTail threw on ' + c.name + ': ' + e.message); failed++; continue; }
  console.log('TAIL ' + c.name + ' :: ' + tail);
}

process.exit(failed ? 1 : 0);
`
