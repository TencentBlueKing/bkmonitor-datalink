// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	ComponentTrigger = "trigger"

	StageStartup      = "startup"
	StageDetect       = "detect"
	StageFatal        = "fatal"
	StageShutdown     = "shutdown"
	StageDecisionACK  = "go_decision_ack"
	StageCapacityDrop = "capacity_drop"
	StageOffsetReset  = "offset_reset"

	ResultStarted     = "started"
	ResultBrokerACK   = "broker_ack"
	ResultSuccess     = "success"
	ResultFailed      = "failed"
	ResultSkipped     = "skipped"
	ResultTimeout     = "timeout"
	ResultDropped     = "dropped"
	ResultReleased    = "released"
	ResultRecovered   = "recovered"
	ResultInvalidated = "invalidated"
)

// Logger emits the fixed event envelope used by alarmd. Metric labels remain
// bounded; diagnostic log attributes may include record coordinates and a
// sanitized error, but never payloads or credentials.
type Logger struct {
	component string
	next      *slog.Logger
	writer    *serializedLogWriter
}

// BoundedLogPolicy always records one-time lifecycle transitions. Routine
// success is omitted. Repeated transitions, recovery and exceptional results
// use a concrete limiter: phase one buckets by reason or reason-empty stage,
// phase two additionally buckets by Query Group scope and reports suppressed
// counts. M8 does not hard-code a sampling rate or time window before G3
// calibration.
type BoundedLogPolicy struct {
	repeated RepeatedLogLimiter
}

func NewBoundedLogPolicy(limiter *WindowLogLimiter) (*BoundedLogPolicy, error) {
	if limiter == nil {
		return nil, errors.New("observability: bounded log policy requires a window limiter")
	}
	return &BoundedLogPolicy{repeated: limiter}, nil
}

// NewScopedBoundedLogPolicy builds the phase-two policy that limits repeated
// lines per (reason, Query Group) bucket and records merged counts.
func NewScopedBoundedLogPolicy(limiter *ScopedLogLimiter) (*BoundedLogPolicy, error) {
	if limiter == nil {
		return nil, errors.New("observability: bounded log policy requires a scoped limiter")
	}
	return &BoundedLogPolicy{repeated: limiter}, nil
}

func (p *BoundedLogPolicy) ShouldLog(observation Observation) bool {
	return p.Admit(observation).Allowed
}

// Admit decides whether the observation is logged and how many earlier lines
// of the same bucket were merged into it.
func (p *BoundedLogPolicy) Admit(observation Observation) LogAdmission {
	if mandatoryLogStage(observation.Stage) {
		return LogAdmission{Allowed: true}
	}
	if !repeatedLogObservation(observation) || p == nil || p.repeated == nil {
		return LogAdmission{}
	}
	return p.repeated.Admit(observation)
}

type LoggingObserver struct {
	logger *Logger
	policy *BoundedLogPolicy
}

func NewLoggingObserver(logger *Logger, policy *BoundedLogPolicy) *LoggingObserver {
	return &LoggingObserver{logger: logger, policy: policy}
}

func New(component string, writer io.Writer) *Logger {
	if writer == nil {
		writer = io.Discard
	}
	locked := &serializedLogWriter{next: writer}
	return &Logger{
		writer:    locked,
		component: component,
		next: slog.New(slog.NewJSONHandler(locked, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})),
	}
}

func Discard(component string) *Logger {
	return New(component, io.Discard)
}

func (l *Logger) Info(stage, result string, records int, duration time.Duration, attrs ...slog.Attr) {
	l.log(slog.LevelInfo, stage, result, records, duration, attrs...)
}

func (l *Logger) Warn(stage, result string, records int, duration time.Duration, attrs ...slog.Attr) {
	l.log(slog.LevelWarn, stage, result, records, duration, attrs...)
}

func (l *Logger) Error(stage, result string, records int, duration time.Duration, attrs ...slog.Attr) {
	l.log(slog.LevelError, stage, result, records, duration, attrs...)
}

func (l *LoggingObserver) Observe(ctx context.Context, observation Observation) {
	// These facts feed complete counters/gauges. TargetFlow already carries the
	// selected run's decision; do not spend ordinary log quota on every update.
	if observation.Component == ComponentScheduler {
		switch observation.Stage {
		case StageRunnerReturned, StageDispatcherSnapshot, StageQueryPermitWait, StageExpiredRangeReturned:
			return
		case StageSlotWait:
			// Every wait is measured; only a slow one is written down. The
			// ordinary case is thousands a second of a few milliseconds each,
			// answers no question anyone asks, and would push out the lines
			// that do.
			if observation.Duration < SlowSlotWait {
				return
			}
		}
	}
	// A catalog object read is the same kind of fact: one per read, hit
	// included, three hundred a second on a live replica, and complete in
	// object_read_total{kind,result}. It was never written before it had a
	// name -- an unlisted stage is not a workflow stage -- and getting its
	// name must not turn it into the largest line in the log.
	if observation.Component == ComponentControlPlane && observation.Stage == StageObjectRead {
		return
	}
	if l == nil || l.logger == nil || l.logger.next == nil || l.policy == nil {
		return
	}
	observation = NormalizeObservation(observation)
	// Merge the context coordinates before admission so a scoped limiter sees
	// the Query Group the Coordinator attached to ctx.
	observation.Trace = mergeTraceFields(observation.Trace, TraceFieldsFromContext(ctx))
	admission := l.policy.Admit(observation)
	if !admission.Allowed {
		return
	}
	l.logger.logObservation(ctx, observation, admission)
}

func (l *Logger) logObservation(ctx context.Context, observation Observation, admission LogAdmission) {
	// Terminal scheduler errors bypass the worker's internal observations.
	// Read typed evidence from the returned error before text is truncated.
	if observation.GapConflict == nil && observation.Err != nil {
		var conflict interface{ GapConflictEvidence() *GapExtensionFacts }
		if errors.As(observation.Err, &conflict) {
			observation.GapConflict = conflict.GapConflictEvidence()
		}
	}
	observation.Trace = mergeTraceFields(observation.Trace, TraceFieldsFromContext(ctx))
	attributes := []slog.Attr{
		slog.String("component", string(observation.Component)),
		slog.String("stage", string(observation.Stage)),
		slog.String("result", string(observation.Result)),
		slog.String("reason_code", string(observation.ReasonCode)),
		slog.String("operation", string(observation.Operation)),
		slog.String("direction", string(observation.Direction)),
		slog.Int64("duration_ms", observation.Duration.Milliseconds()),
	}
	if f := observation.GapExtensions; len(f) > 0 {
		attributes = append(attributes, slog.Any("gap_extensions", f))
	}
	if f := observation.GapConflict; f != nil {
		attributes = append(attributes, slog.Any("gap_conflict", f))
	}
	if f := observation.QueryCooldown; f != nil {
		attributes = append(attributes, slog.Any("query_cooldown", f))
	}
	if f := observation.QueryTiming; f != nil {
		attributes = append(attributes, slog.Any("query_timing", f))
	}
	if f := observation.ShortPeriodCompletion; f != nil {
		attributes = append(attributes, slog.Any("short_period_completion", f))
	}
	if r := observation.OutputRejection; r != nil {
		attributes = append(attributes, slog.Any("output_rejection", r))
	}
	if w := observation.OutputWrite; w != nil {
		// Both numbers, zero included: a success that handed the broker
		// nothing is the case this exists to tell from a write.
		attributes = append(attributes, slog.Int64("messages_published", w.Published), slog.Int64("events_without_message", w.WithoutMessage))
		// The breakdown under its own keys, only the buckets that counted:
		// which protocol dropped which kind, readable from the line itself.
		for _, bucket := range w.WithoutMessageBy {
			if bucket.Events > 0 {
				attributes = append(attributes, slog.Int64("events_without_message_"+bucket.Format+"_"+strings.ToLower(bucket.EventKind), bucket.Events))
			}
		}
	}
	if observation.OutputWireFormat != "" {
		attributes = append(attributes, slog.String("wire_format", observation.OutputWireFormat))
	}
	if matched := observation.PlanSeriesMatched; matched != nil {
		// Zero rendered: a Plan bound to no series is the reading this key
		// exists for, and it is the line a search for the strategy finds.
		attributes = append(attributes, slog.Int("series_matched", *matched))
	}
	if p := observation.PrimaryInput; p != nil {
		// What the query answered, on the completion line: the fact a hole on
		// a window is read against. EMPTY is rendered, not omitted -- it is
		// the reading that says every series was absent from this minute.
		attributes = append(attributes, slog.String("primary_completeness", p.Completeness))
		if p.DataState != "" {
			attributes = append(attributes, slog.String("primary_data_state", p.DataState))
		}
	}
	// How much of the object this round described, on the line, whenever it
	// described less than all of it. Gated on the two counts and not on the
	// named windows: the round these exist for is the one that summarised no
	// window at all -- every series resumed, or none of their State loadable
	// -- and the block below is gated on a named window, so that round is
	// silent there by construction. A count visible only when the thing whose
	// absence it explains is present explains nothing.
	//
	// Levels rides along so the identity a reader checks -- levels plus these
	// two against the series the Slot handled -- is on one line rather than a
	// join across three.
	if c := observation.HistoryCoverage; c != nil && (c.Resumed > 0 || c.Constrained > 0) {
		attributes = append(attributes,
			slog.Uint64("history_levels", uint64(c.Levels)),
			slog.Uint64("history_resumed", uint64(c.Resumed)),
			slog.Uint64("history_constrained", uint64(c.Constrained)))
	}
	if c := observation.HistoryCoverage; c != nil && len(c.Windows) > 0 {
		// The worst named window, on the line: which series, and which minutes
		// it lacks. One window rather than all of them, because the line is
		// read by a person searching for one strategy and the rest are on the
		// row; the count says how many more were named.
		worst := c.Windows[0]
		attributes = append(attributes,
			slog.String("history_worst_series", worst.Series),
			slog.Int64("history_worst_end", worst.End),
			slog.Int("history_windows_named", len(c.Windows)))
		if len(worst.Missing) > 0 {
			attributes = append(attributes, slog.String("history_worst_missing", joinInt64(worst.Missing)))
		}
		if len(worst.Unusable) > 0 {
			attributes = append(attributes, slog.String("history_worst_unusable", joinInt64(worst.Unusable)))
		}
		if worst.GuardReason != "" {
			attributes = append(attributes, slog.String("history_worst_guard", worst.GuardReason))
		}
	}
	if rejected := observation.HistoryCoverageRejected; rejected != nil {
		// The reading the server declined, on the line where the reading
		// would have been: the rule, and the window's series when the rule
		// is about one. A grep for the rule finds every round it refused.
		attributes = append(attributes, slog.String("history_coverage_rejected", string(rejected.Rule)))
		if rejected.Series != "" {
			attributes = append(attributes, slog.String("history_coverage_rejected_series", rejected.Series))
		}
	}
	if counts := observation.OutputWireFormats; len(counts) > 0 {
		// One key per format the batch carried, under the format's own
		// name: a grep for standard_raw_event finds the batches that sent
		// one, and finds nothing only when none did.
		for _, format := range sortedWireFormats(counts) {
			attributes = append(attributes, slog.Int64("wire_format_events_"+format, counts[format]))
		}
	}
	if counts := observation.OutputEventKinds; len(counts) > 0 {
		// The same events once more by kind, under the format's and the
		// kind's own names, so a search for the recovery that should have
		// left on the standard line finds the batch it left in.
		keys := make([]OutputEventKindKey, 0, len(counts))
		for key := range counts {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].Format != keys[j].Format {
				return keys[i].Format < keys[j].Format
			}
			return keys[i].EventKind < keys[j].EventKind
		})
		for _, key := range keys {
			attributes = append(attributes, slog.Int64("wire_format_events_"+key.Format+"_"+strings.ToLower(key.EventKind), counts[key]))
		}
	}
	if f := observation.FrozenStateRenewal; f != nil {
		// The eight numbers on the line, not only on the metric: the line is
		// what a reader of one Slot has, and without them frozen_state_renewed
		// said a renewal happened and nothing about it.
		attributes = append(attributes, slog.Any("frozen_state_renewal", f))
	}
	if f := observation.DispatchTurnaway; f != nil {
		attributes = append(attributes, slog.Any("dispatch_turnaway", f))
	}
	if f := observation.ViewStream; f != nil {
		attributes = append(attributes, slog.Any("view_stream", f))
	}
	if f := observation.StateAlreadyApplied; f != nil && !f.Empty() {
		for key, count := range f.Counts {
			attributes = append(attributes, slog.Int64("state_already_applied_"+string(key.Site)+"_"+string(key.Kind), count))
		}
		if f.Skew != nil {
			attributes = append(attributes, slog.Any("state_revision_skew", f.Skew))
		}
	}
	if f := observation.StateVersionConflict; f != nil && !f.Empty() {
		for key, count := range f.Counts {
			attributes = append(attributes, slog.Int64("state_version_conflict_"+string(key.Site)+"_"+string(key.Kind), count))
		}
		if len(f.Samples) > 0 {
			attributes = append(attributes, slog.Any("state_version_conflict_samples", f.Samples))
		}
	}
	if f := observation.StateApplyChunk; f != nil {
		attributes = append(attributes, slog.Int("chunk_index", f.Index), slog.Int("chunk_count", f.Count),
			slog.Int64("applied_keys", f.AppliedKeys), slog.Int64("applied_bytes", f.AppliedBytes), slog.Int64("elapsed_ms", f.ElapsedMillis))
		if len(f.RefusalRules) > 0 {
			attributes = append(attributes, slog.String("state_refusal_rules", strings.Join(f.RefusalRules, ",")))
		}
		if f.LegacyRecordIDs > 0 {
			attributes = append(attributes, slog.Int("state_legacy_record_ids", f.LegacyRecordIDs))
		}
	}
	if f := observation.CapacityRejection; f != nil {
		attributes = append(attributes, slog.String("capacity_phase", f.Phase), slog.Uint64("capacity_shared_used", f.SharedUsed), slog.Uint64("capacity_requested", f.Requested), slog.Uint64("capacity_limit", f.Limit))
		if f.OwnUsed != nil {
			attributes = append(attributes, slog.Uint64("capacity_own_used", *f.OwnUsed))
		}
		// Every budget's own usage, so the refusal can be read against the ones
		// that did not refuse. capacity_budget names which was reached; without
		// these the line cannot say whether the rest were anywhere near theirs.
		for _, usage := range f.Usage {
			attributes = append(attributes,
				slog.Uint64("capacity_own_"+string(usage.Budget), usage.OwnUsed),
				slog.Uint64("capacity_limit_"+string(usage.Budget), usage.Limit))
		}
	}
	if f := observation.SlotBudgetUsage; f != nil {
		attributes = append(attributes, slog.Any("slot_budget_usage", f))
	}
	// Flat keys rather than an object, under its own condition: the two
	// answer different questions and a row can carry either without the
	// other.
	//
	// Flat because this line has already paid for the other choice once. A
	// nested held_by shipped here and was invisible to anything filtering the
	// line, and the rows that most needed it carried no cause at all. This
	// field exists to be filtered on -- "which Slots spent their period
	// waiting for records" is the question -- so it is written the way the
	// rest of the line is. The neighbour above is the known exception: it
	// predates this and is nested, so its members are readable on a row and
	// not selectable by one.
	if f := observation.SlotTiming; f != nil {
		attributes = append(attributes,
			slog.Uint64("slot_millis", f.Slot),
			slog.Uint64("input_millis", f.Input),
			slog.Uint64("preflight_millis", f.Preflight),
			slog.Uint64("evaluate_millis", f.Evaluate))
	}
	if observation.CapacityBudget != "" {
		attributes = append(attributes, slog.String("capacity_budget", string(observation.CapacityBudget)))
	}
	if observation.SourceKind != "" {
		attributes = append(attributes, slog.String("source_kind", string(observation.SourceKind)))
	}
	attributes = appendObservationCounts(attributes, observation.Counts)
	attributes = appendEnvelopePassCounts(attributes, observation)
	attributes = appendTraceFields(attributes, observation.Trace)
	if f := observation.QueryFailure; f != nil {
		attributes = append(attributes, slog.String("failure_stage", f.Stage), slog.String("failure_category", f.Category), slog.String("failure_code", f.Code))
		if f.Detail != "" {
			attributes = append(attributes, slog.String("failure_detail", f.Detail))
		}
	}
	if observation.RuntimeConfig != nil {
		attributes = append(attributes, slog.Any("runtime_config", observation.RuntimeConfig))
	}
	if facts := observation.SourceWithheld; facts != nil {
		attributes = append(attributes,
			slog.String("withheld_disposition", facts.Disposition),
			slog.String("withheld_reason", facts.Reason),
		)
		if facts.Field != "" {
			attributes = append(attributes, slog.String("withheld_field", facts.Field))
		}
		if facts.Dropped > 0 {
			attributes = append(attributes, slog.Int("withheld_dropped", facts.Dropped))
		}
	}
	if facts := observation.NoDataCensus; facts != nil {
		// Written even when it is none, which is the whole reason it is here.
		// Somebody looking for why no-data detection reported nothing greps
		// this stage and finds no line at all, and no line means either "this
		// worker has no such Plan" or "it had them and none reached a
		// decision". The zero is the answer to that question.
		attributes = append(attributes, slog.Int("no_data_plans", facts.Plans))
	}
	if facts := observation.ScheduleCutover; facts != nil && facts.Result != "success" {
		// Both, and on every failure. A cutover that says only that it failed
		// leaves a reader with a whole population to search and no cause; these
		// two are the difference between that and one line to act on.
		attributes = append(attributes,
			slog.String("cutover_reason", facts.Reason),
			slog.String("cutover_query_group", facts.QueryGroup),
		)
	}
	if facts := observation.SlotWait; facts != nil {
		// Reached only for a wait past the threshold, which is the attempt
		// that has something to explain.
		attributes = append(attributes, slog.String("slot_wait", facts.Wait))
	}
	if facts := observation.RangeGate; facts != nil {
		// The word plus the values it was derived from. Without the values the
		// word cannot be checked, and this word exists precisely because the
		// previous reading -- a GAP_SKIPPED completion -- could not be.
		attributes = append(attributes,
			slog.String("range_gate", facts.Outcome),
			slog.Int64("range_gate_progress_next_slot", facts.ProgressNextSlot),
			slog.Int64("range_gate_expected_next_slot", facts.ExpectedNextSlot),
			slog.Bool("range_gate_unfinished_slot", facts.UnfinishedSlotPresent),
		)
		if facts.UnfinishedSlotPresent {
			attributes = append(attributes,
				slog.Int64("range_gate_unfinished_evaluation_time", facts.UnfinishedSlotEvaluationTime))
		}
		// The two candidate bounds, on the refusals that computed them. They
		// were on the facts and on no line: the word steps_below_one says the
		// range would be shorter than two Slots and nothing about which of the
		// two bounds held it there, which is the difference between a Query
		// Group barely past its window and one whose deadline has only just
		// passed. Not emitted as zeroes when they were never computed, for the
		// same reason bounds_known exists at all.
		if facts.BoundsKnown {
			attributes = append(attributes,
				slog.Bool("range_gate_bounds_known", true),
				slog.Int64("range_gate_distance_bound", facts.DistanceBound),
				slog.Int64("range_gate_deadline_bound", facts.DeadlineBound),
			)
		}
	}
	if facts := observation.ReplayExpiry; facts != nil {
		// The reason on every expiry, and the two compared instants on the one
		// that reports a defect. A Slot that says only that it was skipped
		// leaves the reader unable to tell a worker that fell behind from a
		// readiness rule that will skip every Slot of that period for ever.
		attributes = append(attributes,
			slog.String("replay_expiry_reason", facts.Reason),
			slog.Uint64("replay_distance", uint64(facts.Distance)),
		)
		if facts.DistanceBoundaryUnixMilli != 0 {
			attributes = append(attributes,
				slog.Int64("replay_ready_at", facts.ReadyAtUnixMilli),
				slog.Int64("replay_distance_boundary", facts.DistanceBoundaryUnixMilli),
			)
		}
		attributes = appendHeldByAttributes(attributes, facts.HeldBy)
	}
	attributes = appendHeldByAttributes(attributes, observation.HeldBy)
	attributes = appendSlotCompletionKind(attributes, observation.SlotCompletionKind)
	if facts := observation.SegmentContent; facts != nil {
		attributes = append(attributes, slog.String("segment_content", facts.State))
	}
	if facts := observation.NoDataSlot; facts != nil {
		attributes = append(attributes,
			slog.String("no_data_outcome", facts.Outcome),
			slog.Int("no_data_outcome_plans", facts.Plans),
		)
	}
	if facts := observation.NoDataAbsence; facts != nil {
		// Every count, zeros included: the horizon a deployment has switched
		// on shows up here as expired and suppressed moving off zero, and a
		// line that omitted the zeros would leave "nothing expired" and "this
		// build does not report expiry" indistinguishable.
		attributes = append(attributes,
			slog.String("no_data_outcome", facts.Outcome),
			slog.Int64("no_data_horizon_seconds", facts.HorizonSeconds),
			slog.String("no_data_horizon_source", facts.HorizonSource),
			slog.String("no_data_roster_source", facts.RosterSource),
			slog.Uint64("no_data_expected", facts.Expected),
			slog.Uint64("no_data_present", facts.Present),
			slog.Uint64("no_data_absent", facts.Absent),
			slog.Uint64("no_data_unavailable", facts.Unavailable),
			slog.Uint64("no_data_dropped", facts.Dropped),
			slog.Uint64("no_data_expired", facts.Expired),
			slog.Uint64("no_data_suppressed", facts.Suppressed),
			// The ages beside the count, zeros included, for the same reason
			// as the counts: whether the horizon can reach anything is read
			// from the last bucket being zero or not.
			slog.Uint64("no_data_absent_this_round", facts.AbsentAges.ThisRound),
			slog.Uint64("no_data_absent_under_hour", facts.AbsentAges.UnderHour),
			slog.Uint64("no_data_absent_under_day", facts.AbsentAges.UnderDay),
			slog.Uint64("no_data_absent_day_or_more", facts.AbsentAges.DayOrMore),
		)
	}
	if facts := observation.TargetResolution; facts != nil {
		attributes = append(attributes,
			slog.String("strategy_id", facts.StrategyID),
			slog.String("target_resolution", facts.State),
			slog.Int("target_selectors", len(facts.Selectors)),
		)
		if facts.StaleAgeSeconds > 0 {
			attributes = append(attributes, slog.Int64("resolved_from_stale_snapshot_age_seconds", facts.StaleAgeSeconds))
		}
		for _, selector := range facts.Selectors {
			if selector.State == "OK" && !selector.NodeMissing && !selector.NodeForeign {
				continue
			}
			// Only the selectors with something to say are on the line: an
			// unavailable or incomplete one, an empty one, a dangling node.
			attributes = append(attributes, slog.String("target_selector",
				selector.Kind+" "+selector.ID+" "+selector.State+" "+selector.Reason+
					" kept="+strconv.Itoa(selector.Kept)+" dropped="+strconv.Itoa(selector.Dropped)))
		}
	}
	if observation.GapApplySite != "" {
		attributes = append(attributes, slog.String("gap_apply_site", observation.GapApplySite))
	}
	if facts := observation.GapStatements; facts != nil {
		attributes = append(attributes,
			slog.String("gap_statements_shape", facts.Shape),
			slog.Int("gap_statements", facts.Statements),
			slog.String("gap_statements_before_events", strings.Join(facts.BeforeEvents, ",")),
			slog.String("gap_statements_after_state", strings.Join(facts.AfterState, ",")))
	}
	if facts := observation.GapProgress; facts != nil {
		// Both numbers, on every line. The question these answer is "how far
		// has this guard got", and k alone answers it only for a reader who
		// already knows N -- which varies by strategy, because it is the
		// largest history requirement across that strategy's Levels.
		attributes = append(attributes,
			slog.String("gap_scope", facts.Scope),
			slog.String("gap_scope_status", facts.Status),
			slog.String("gap_scope_reason", facts.Reason),
			slog.Uint64("gap_full_slots_required", uint64(facts.Required)),
			slog.Uint64("gap_full_slots_observed", uint64(facts.Observed)),
			slog.String("gap_progress", facts.Progress),
		)
	}
	if facts := observation.NoDataMemoryWrite; facts != nil {
		// Both, always. The outcome alone makes a reader remember which of the
		// five mean the record was kept, and that is the question they came
		// with; stored alone loses which situation it was.
		attributes = append(attributes,
			slog.String("no_data_memory_outcome", facts.Outcome),
			slog.Bool("no_data_memory_stored", facts.Stored),
		)
		if facts.DerivedFrom != "" {
			attributes = append(attributes, slog.String("no_data_memory_derived_from", facts.DerivedFrom))
		}
		if conflict := facts.Conflict; conflict != nil {
			// Which comparison failed and both sides of it. A conflict carries
			// no reason code -- it is not a rejection -- so without these the
			// line says the write was refused and nothing about why, which is
			// what a fleet-wide refusal looked like for a day.
			attributes = append(attributes,
				slog.String("no_data_memory_conflict", conflict.Kind),
				slog.Uint64("no_data_memory_expected_revision", conflict.ExpectedRevision),
				slog.Uint64("no_data_memory_stored_revision", conflict.StoredRevision),
			)
			if conflict.Persisted != "" || conflict.Proposed != "" {
				attributes = append(attributes,
					slog.String("no_data_memory_persisted_digest", conflict.Persisted),
					slog.String("no_data_memory_proposed_digest", conflict.Proposed),
				)
			}
		}
	}
	if facts := observation.NoDataMemoryRefusal; facts != nil {
		attributes = append(attributes, slog.String("no_data_memory_refusal", facts.Reason))
		if facts.Record != "" {
			// Both numbers together or neither. A reader given the measurement
			// with no bound, or the bound with no measurement, cannot tell how
			// far over it is, which is the only question this line exists to
			// answer.
			attributes = append(attributes,
				slog.String("no_data_memory_record", facts.Record),
				slog.Int("no_data_memory_groups", facts.Groups),
				slog.Int("no_data_memory_limit", facts.Limit),
			)
		}
	}
	if facts := observation.StateGenerationSkew; facts != nil {
		attributes = append(attributes,
			slog.String("state_generation_skew_kind", facts.Kind),
			slog.String("state_generation_skew_strategy_id", facts.StrategyID),
		)
	}
	if facts := observation.StateCarry; facts != nil {
		attributes = append(attributes,
			slog.String("state_carry_scope", facts.Scope),
			slog.String("state_carry_result", facts.Result),
			slog.Int("state_carry_count", facts.Count),
		)
	}
	if facts := observation.ActivationHold; facts != nil {
		attributes = append(attributes,
			slog.Int("activation_reappeared", facts.Reappeared),
			slog.Int("activation_held", facts.Held),
			slog.Int64("activation_held_max_age_seconds", facts.MaxAgeSeconds),
			slog.Bool("activation_held_samples_truncated", facts.Truncated),
			slog.Any("activation_held_samples", facts.Samples),
		)
	}
	if facts := observation.DrainingQG; facts != nil {
		attributes = append(attributes,
			slog.Int("draining_total", facts.Total),
			slog.Int("draining_undrained", facts.Undrained),
			slog.Int("draining_isolated", facts.Isolated),
			slog.Int("draining_retired", facts.Retired),
			slog.Int("draining_cursor_pruned", facts.CursorPruned),
			slog.Bool("draining_samples_truncated", facts.Truncated),
			slog.Any("draining_samples", facts.Samples),
		)
	}
	if facts := observation.Rebalance; facts != nil {
		attributes = append(attributes,
			slog.Int("rebalance_ready_workers", facts.ReadyWorkers),
			slog.Int("rebalance_assigned", facts.Assigned),
			slog.Int("rebalance_target", facts.Target),
			slog.Int("rebalance_most_owned", facts.MostOwned),
			slog.Int("rebalance_least_owned", facts.LeastOwned),
			slog.Int("rebalance_batch", facts.Batch),
			slog.Int("rebalance_planned_moves", facts.PlannedMoves),
			slog.Int("rebalance_published_moves", facts.PublishedMoves),
			slog.Int("rebalance_conflicts", facts.Conflicts),
			slog.Bool("rebalance_paused", facts.Paused),
			slog.Float64("rebalance_paused_for_seconds", facts.PausedForSeconds),
			slog.Bool("rebalance_owned_truncated", facts.Truncated),
			slog.Any("rebalance_owned", facts.Owned),
			slog.Bool("rebalance_moves_truncated", facts.MovesTruncated),
			slog.Any("rebalance_moves", facts.Moves),
		)
		if bytes := facts.Bytes; bytes != nil {
			// The byte-constraint round's counts, zeros included, for the
			// same reason: judged and unread together say whether the round
			// could judge at all, and a round that moved nothing because it
			// judged nothing reads identically to one that found nothing to
			// move without them. The per-Worker lists stay on the page; the
			// overloaded one is here because it is the actionable half and
			// is bounded by the fleet.
			attributes = append(attributes,
				slog.Int("byte_constraint_judged", bytes.Judged),
				slog.Int("byte_constraint_unread", bytes.Unread),
				slog.Int("byte_constraint_pool_unknown", len(bytes.PoolUnknown)),
				slog.Int("byte_constraint_unsettled", len(bytes.Unsettled)),
				slog.Any("byte_constraint_overloaded", bytes.Overloaded),
				slog.Int("byte_constraint_planned_moves", bytes.PlannedMoves),
				slog.Int("byte_constraint_published_moves", bytes.PublishedMoves),
			)
		}
		if gate := facts.ShardAware; gate != nil {
			// All three whenever the gate ran, zeros included: a round that
			// admits a split and one from a build that has no gate read the
			// same otherwise, and "how many ready workers did it ask" is the
			// denominator of the other two. The list stays beside the count
			// because a rollout reader wants the replica, not the number.
			attributes = append(attributes,
				slog.Int("shard_aware_ready", gate.Ready),
				slog.Int("shard_unaware_replicas", len(gate.Unaware)),
				slog.Any("shard_unaware", gate.Unaware),
				slog.Int("shard_splits_held", gate.SplitsHeld),
			)
		}
	}
	if facts := observation.AssignmentSweep; facts != nil {
		// The five numbers of a sweep, zeros included: the line existed for a
		// release with only its stage and result on it, and "swept" with
		// nothing beside it could not be told from "swept nothing".
		attributes = append(attributes,
			slog.Int("assignment_sweep_scanned", facts.Scanned),
			slog.Int("assignment_sweep_retired", facts.Retired),
			slog.Int("assignment_sweep_reclaimed", facts.Reclaimed),
			slog.Int("assignment_sweep_held_by_lease", facts.HeldByLease),
			slog.Int("assignment_sweep_changed", facts.Changed),
		)
	}
	if facts := observation.AssignmentIndex; facts != nil {
		attributes = append(attributes,
			slog.Uint64("assignment_index_round", facts.Round),
			slog.Uint64("assignment_index_control_epoch", facts.ControlEpoch),
			slog.Int("assignment_index_workers", facts.Workers),
			slog.Int("assignment_index_rewritten", facts.Rewritten),
			slog.Int("assignment_index_missing", facts.Missing),
			slog.String("assignment_index_result", facts.Result),
			slog.Int("assignment_index_stale_rounds", facts.StaleRounds),
			slog.Bool("assignment_index_set_read", facts.SetRead),
			slog.Int("assignment_index_candidates", facts.Candidates),
			slog.Int("assignment_index_assigned", facts.Assigned),
			slog.Int("assignment_index_opened", facts.Opened),
			slog.Int("assignment_index_rejected", facts.Rejected),
			slog.Int("assignment_index_released", facts.Released),
			slog.Int("assignment_index_retained", facts.Retained),
			slog.Int("assignment_index_reads", facts.Reads),
			slog.Bool("assignment_index_full_read", facts.FullRead),
		)
	}
	if facts := observation.CursorAdvance; facts != nil {
		attributes = append(attributes,
			slog.Int64("cursor_advance_from", facts.From),
			slog.Int64("cursor_advance_to", facts.To),
			slog.String("cursor_advance_status", facts.Status),
			slog.String("cursor_advance_refusal", facts.Refusal),
			slog.Int64("cursor_advance_in_flight_slot", facts.InFlightSlot),
		)
	}
	if facts := observation.SourceRefresh; facts != nil {
		attributes = append(attributes,
			slog.String("source_refresh_status", string(facts.Status)),
			slog.Bool("source_refresh_counts_known", facts.CountsKnown),
		)
		if facts.ObservationID != "" {
			attributes = append(attributes, slog.String("source_observation_id", facts.ObservationID))
		}
		if facts.SnapshotRevision != "" {
			attributes = append(attributes, slog.String("snapshot_revision", facts.SnapshotRevision))
		}
		if facts.PublicationEpoch > 0 {
			attributes = append(attributes, slog.Uint64("publication_epoch", facts.PublicationEpoch))
		}
		attributes = append(attributes,
			slog.Int("source_compiled_strategies", facts.CompiledStrategies),
			slog.Int("source_reused_strategies", facts.ReusedStrategies),
		)
		if facts.RetainedStaleRevisions > 0 {
			attributes = append(attributes, slog.Int("source_retained_stale_revisions", facts.RetainedStaleRevisions))
		}
		if facts.ReadMode != "" {
			attributes = append(attributes,
				slog.String("source_read_mode", string(facts.ReadMode)),
				slog.String("source_read_reason", string(facts.ReadReason)),
				slog.Int("source_strategies_read", facts.StrategiesRead),
				slog.Bool("source_change_signal_present", facts.ChangeSignalPresent),
			)
			if facts.ChangeSignalPresent {
				attributes = append(attributes, slog.Int64("source_change_signal_age_seconds", facts.ChangeSignalAgeSeconds))
			}
		}
		// Named apart from snapshot_revision on purpose: a round that published
		// nothing still knows what the fleet is executing, and writing that under
		// the published name is what makes a normal lag read as a stall.
		if facts.ActivatedRevision != "" {
			attributes = append(attributes, slog.String("activated_snapshot_revision", facts.ActivatedRevision))
		}
		if facts.ActivatedEpoch > 0 {
			attributes = append(attributes, slog.Uint64("activated_publication_epoch", facts.ActivatedEpoch))
		}
		if facts.ActivationCaughtUp {
			attributes = append(attributes, slog.Bool("activation_caught_up", true))
		}
		if facts.ActivationRebuilt {
			attributes = append(attributes, slog.Bool("activation_rebuilt", true))
		}
		if facts.ActiveQueryGroupsKnown {
			attributes = append(attributes, slog.Int("active_query_groups", facts.ActiveQueryGroups))
		}
		if facts.CountsKnown {
			attributes = append(attributes,
				slog.Int("old_query_groups", facts.OldQueryGroups),
				slog.Int("new_query_groups", facts.NewQueryGroups),
				slog.Int("added_query_groups", facts.AddedQueryGroups),
				slog.Int("retired_query_groups", facts.RetiredQueryGroups),
			)
		}
	}
	if facts := observation.ActivationFailure; facts != nil {
		attributes = append(attributes,
			slog.String("activation_failure_stage", string(facts.Stage)),
			slog.String("activation_failure_class", string(facts.Class)),
		)
		if facts.Stage == ActivationFailureStageReactivation {
			attributes = append(attributes,
				slog.Int("draining_query_groups", facts.DrainingQueryGroups),
				slog.Int("candidate_query_groups", facts.CandidateQueryGroups),
				slog.Int("reappeared_query_groups", facts.ReappearedQueryGroups),
			)
			if facts.Class == ActivationFailureClassNotDrained {
				attributes = append(attributes,
					slog.Bool("reappeared_query_group_samples_truncated", facts.ReappearedQueryGroupSamplesTruncated),
					slog.Any("reappeared_query_group_samples", facts.ReappearedQueryGroupSamples),
				)
			}
		}
	}
	if facts := observation.ControlSourceRound; facts != nil {
		attributes = append(attributes,
			slog.String("control_source_outcome", facts.Outcome),
			slog.String("control_source_exit", facts.Exit),
		)
	}
	if len(observation.AlgorithmEvaluations) > 0 {
		attributes = append(attributes, slog.Any("algorithm_evaluations", observation.AlgorithmEvaluations))
	}
	if len(observation.AlgorithmInputs) > 0 {
		attributes = append(attributes, slog.Any("algorithm_inputs", observation.AlgorithmInputs))
	}
	if len(observation.LevelOutcomes) > 0 {
		attributes = append(attributes, slog.Any("level_outcomes", observation.LevelOutcomes))
	}
	if facts := observation.Shardability; facts != nil {
		// The denominator first: a count of strategies that cannot be split
		// says nothing without how many there are.
		attributes = append(attributes,
			slog.Int("shardable_plans", facts.Plans),
			slog.Int("shardable_splittable", facts.Splittable),
			slog.Int("shardable_disjunctive", facts.Disjunctive),
			slog.Int("shardable_not_structured", facts.NotStructured),
			slog.Int("shardable_no_queries", facts.NoQueries),
		)
	}
	if facts := observation.SplitPlan; facts != nil {
		// The decision and every reading it was judged from. "This object was
		// not split" is the same line for an object under its share and one
		// whose heaviest value cannot be divided, and those call for opposite
		// answers, so the numbers travel with the word.
		attributes = append(attributes,
			slog.String("split_outcome", facts.Outcome),
			slog.Uint64("split_peak_bytes", facts.PeakBytes),
			slog.Uint64("split_share_bytes", facts.ShareBytes),
			slog.Int("split_shards", facts.Shards),
			slog.Int("split_carrying_shards", facts.Carrying),
			slog.Bool("split_shards_capped", facts.ShardsCapped),
			slog.String("split_dimension", facts.Dimension),
			slog.Int("split_dimension_candidates", facts.Candidates),
			slog.Uint64("split_series", uint64(facts.Series)),
			slog.Int64("split_census_age_seconds", facts.CensusAgeSeconds),
			slog.Int64("split_census_age_bound_seconds", facts.CensusAgeBoundSeconds),
			slog.String("split_census_age_bound_source", facts.CensusAgeBoundSource),
			slog.String("split_census_source", facts.CensusSource),
			slog.Uint64("split_heaviest_value_series", uint64(facts.HeaviestValueSeries)),
			slog.Uint64("split_target_series", uint64(facts.TargetSeries)),
			slog.Uint64("split_tail_series", uint64(facts.TailSeries)),
			slog.Int("split_skew_percent", facts.SkewPercent),
			slog.Uint64("split_largest_shard_series", uint64(facts.LargestShardSeries)),
			slog.Uint64("split_smallest_shard_series", uint64(facts.SmallestShardSeries)),
			slog.Int("split_plans_in_group", facts.PlansInGroup),
			slog.Bool("split_dry_run", facts.DryRun),
		)
	}
	if facts := observation.ShardQuery; facts != nil {
		attributes = append(attributes,
			slog.String("shard_query_outcome", facts.Outcome),
			slog.Int("shard_query_shards", facts.Shards),
			slog.Int("shard_query_built", facts.Built),
			slog.Int("shard_query_values", facts.Values),
			slog.Int("shard_query_fallback_values", facts.FallbackValues),
			slog.Int("shard_query_queries", facts.Queries),
		)
		if facts.Detail != "" {
			attributes = append(attributes, slog.String("shard_query_detail", facts.Detail))
		}
	}
	if facts := observation.SplitRound; facts != nil {
		// The round's own three, with their denominator: skipped alone
		// cannot say whether a zero means nothing was left out or nothing
		// was looked at.
		attributes = append(attributes,
			slog.Int("split_round_over_share", facts.OverShare),
			slog.Int("split_round_examined", facts.Examined),
			slog.Int("split_round_skipped", facts.Skipped),
		)
	}
	if facts := observation.DimensionCensus; facts != nil {
		// The gate's two numbers go out with the census itself: a census that
		// appears, or stops appearing, is a candidate decision, and the
		// decision cannot be checked afterwards from the census alone.
		attributes = append(attributes,
			slog.String("census_source", facts.Source),
			slog.String("census_status", facts.Status),
			slog.Uint64("census_series", uint64(facts.Series)),
			slog.Int("census_dimensions", facts.Dimensions),
			slog.Int("census_values", facts.Values),
			slog.Uint64("census_overflow_values", uint64(facts.OverflowValues)),
			slog.Uint64("census_overflow_series", uint64(facts.OverflowSeries)),
			slog.Int("census_bytes", facts.Bytes),
			slog.Int("census_limit", facts.Limit),
			slog.Uint64("census_peak_bytes", facts.PeakBytes),
			slog.Uint64("census_share_bytes", facts.ShareBytes),
		)
	}
	if observation.Err != nil {
		attributes = append(attributes,
			slog.String("error_type", fmt.Sprintf("%T", observation.Err)),
			slog.String("error", SanitizeErrorText(observation.Err.Error())),
		)
	}
	if admission.Sampled {
		// The one line a pacing bucket keeps per window says it is that line,
		// and carries the merged count even when it is zero: on this line a
		// missing count would read the same as "not counted", and the count is
		// what the line is kept for.
		attributes = append(attributes, slog.Bool("sampled", true), slog.Uint64("suppressed_logs", admission.Suppressed))
	} else if admission.Suppressed > 0 {
		attributes = append(attributes, slog.Uint64("suppressed_logs", admission.Suppressed))
	}
	if admission.SuppressedEvicted > 0 {
		attributes = append(attributes, slog.Uint64("suppressed_logs_evicted", admission.SuppressedEvicted))
	}
	level := slog.LevelInfo
	if observation.Result == Result(ResultFailed) || observation.Result == Result(ResultTimeout) {
		level = slog.LevelError
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.next.LogAttrs(ctx, level, "alarmd event", attributes...)
}

func (l *Logger) log(
	level slog.Level,
	stage, result string,
	records int,
	duration time.Duration,
	attrs ...slog.Attr,
) {
	if l == nil || l.next == nil {
		return
	}
	if duration < 0 {
		duration = 0
	}
	fixed := []slog.Attr{
		slog.String("component", l.component),
		slog.String("stage", stage),
		slog.String("result", result),
		slog.Int("records", records),
		slog.Int64("duration_ms", duration.Milliseconds()),
	}
	l.next.LogAttrs(context.Background(), level, "alarmd event", append(fixed, attrs...)...)
}

// maxLoggedErrorBytes bounds the sanitized error text in one log line.
const maxLoggedErrorBytes = 512

// SanitizeErrorText prepares an error message for a structured log line.
// Internal alarmd errors are static strings or carry plan identities, which
// are acceptable log coordinates. Anything shaped like a URL is replaced with
// "<url>" so endpoints, credentials and query strings never reach the log, and
// the result is truncated to maxLoggedErrorBytes on a rune boundary.
func SanitizeErrorText(text string) string {
	text = redactURLs(text)
	if len(text) <= maxLoggedErrorBytes {
		return text
	}
	cut := maxLoggedErrorBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}

func redactURLs(text string) string {
	const marker = "://"
	var builder strings.Builder
	rest := text
	for {
		index := strings.Index(rest, marker)
		if index < 0 {
			builder.WriteString(rest)
			return builder.String()
		}
		schemeStart := index
		for schemeStart > 0 && isURLSchemeByte(rest[schemeStart-1]) {
			schemeStart--
		}
		if schemeStart == index || !isASCIILetter(rest[schemeStart]) {
			// A bare "://" or a scheme that does not start with a letter is not
			// a URL; keep scanning after it.
			builder.WriteString(rest[:index+len(marker)])
			rest = rest[index+len(marker):]
			continue
		}
		end := index + len(marker)
		for end < len(rest) && !isURLTerminatorByte(rest[end]) {
			end++
		}
		// Sentence punctuation directly after a URL belongs to the message.
		for end > index+len(marker) && isURLTrailingPunctuation(rest[end-1]) {
			end--
		}
		builder.WriteString(rest[:schemeStart])
		builder.WriteString("<url>")
		rest = rest[end:]
	}
}

func isASCIILetter(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
}

func isURLSchemeByte(char byte) bool {
	return isASCIILetter(char) || char >= '0' && char <= '9' || char == '+' || char == '-' || char == '.'
}

func isURLTrailingPunctuation(char byte) bool {
	switch char {
	case ':', '.', ',', ';':
		return true
	default:
		return false
	}
}

func isURLTerminatorByte(char byte) bool {
	switch char {
	case ' ', '\t', '\n', '\r', '"', '\'', '<', '>', ')', ']', '}', ',', ';':
		return true
	default:
		return false
	}
}

func appendObservationCounts(attributes []slog.Attr, counts Counts) []slog.Attr {
	values := []struct {
		name  string
		value int64
	}{
		{"messages", counts.Messages}, {"records", counts.Records}, {"plans", counts.Plans},
		{"levels", counts.Levels}, {"events", counts.Events}, {"bytes", counts.Bytes},
		{"keys", counts.Keys}, {"state_bytes", counts.StateBytes}, {"envelope_reads", counts.EnvelopeReads},
		{"envelope_reads_apply", counts.EnvelopeReadsApply},
	}
	for _, value := range values {
		if value.value > 0 {
			attributes = append(attributes, slog.Int64(value.name, value.value))
		}
	}
	return attributes
}

// appendEnvelopePassCounts writes the four the state preflight's second pass
// splits into, at every value including zero.
//
// Zero included, unlike every other count on the line, because these are the
// only ones whose zero is the answer: the migration is over when
// envelope_answered is zero and stays there, and the two corruption counts
// are read to confirm they are zero. A count that appears only when non-zero
// cannot say "none of these happened" -- it reads identically to "nobody
// counted", which is how envelope_reads spent a release being invisible on
// every line where it was zero.
//
// Gated on the stage rather than on the values, because gating on the values
// is the same omission in another shape. Only the preflight line produces
// them, so only it carries them: four keys on every observation in the
// process would be most of a log line spent restating zeros nobody asked.
func appendEnvelopePassCounts(attributes []slog.Attr, observation Observation) []slog.Attr {
	if observation.Stage != StageStatePreflight {
		return attributes
	}
	return append(attributes,
		slog.Int64("envelope_answered", observation.Counts.EnvelopeAnswered),
		slog.Int64("envelope_corrupt", observation.Counts.EnvelopeCorrupt),
		slog.Int64("no_record_yet", observation.Counts.NoRecordYet),
		slog.Int64("frame_corrupt_rescued", observation.Counts.FrameCorruptRescued),
		slog.Int64("frame_corrupt_lost", observation.Counts.FrameCorruptLost),
		slog.Int64("unclassified", observation.Counts.Unclassified),
		slog.Int64("fetch_ms", observation.Counts.StateFetchMillis),
		slog.Int64("decode_ms", observation.Counts.StateDecodeMillis))
}

func appendTraceFields(attributes []slog.Attr, trace TraceFields) []slog.Attr {
	strings := []struct {
		name  string
		value string
	}{
		{"trace_id", trace.TraceID}, {"execution_id", trace.ExecutionID}, {"message_id", trace.MessageID},
		{"query_group_key", trace.QueryGroupKey}, {"strategy_id", trace.StrategyID}, {"business_id", trace.BusinessID},
		{"snapshot_revision", trace.SnapshotRevision}, {"query_revision", trace.QueryRevision},
		{"schedule_revision", trace.ScheduleRevision}, {"due_plan_set_digest", trace.DuePlanSetDigest},
		{"owner_id", trace.OwnerID},
		{"level_id", trace.LevelID}, {"terminal_scope", trace.TerminalScope},
		{"field_path", trace.TerminalFieldPath}, {"record_id", trace.RecordID},
		{"dimension_identity_digest", trace.DimensionIdentityDigest}, {"topic", trace.Topic},
		{"source_window", trace.SourceWindow},
	}
	for _, value := range strings {
		if value.value != "" {
			attributes = append(attributes, slog.String(value.name, value.value))
		}
	}
	if trace.PartitionKnown {
		attributes = append(attributes, slog.Int64("partition", int64(trace.Partition)))
	}
	if trace.OffsetKnown {
		attributes = append(attributes, slog.Int64("offset", trace.Offset))
	}
	if trace.OwnerEpoch > 0 {
		attributes = append(attributes, slog.Uint64("owner_epoch", trace.OwnerEpoch))
	}
	if trace.EvaluationTime > 0 {
		attributes = append(attributes, slog.Int64("evaluation_time", trace.EvaluationTime))
	}
	if trace.ScheduleSegmentStart > 0 {
		attributes = append(attributes, slog.Int64("schedule_segment_start", trace.ScheduleSegmentStart))
	}
	return attributes
}

func mandatoryLogStage(stage Stage) bool {
	switch stage {
	case StageStartup, StageConfigLoaded, StageKafkaAssigned, StageShutdown, StageFatal:
		return true
	case StageSnapshotRefreshed, StageSnapshotUnavailable, StageAssignmentAcquired, StageAssignmentLost,
		StageTakeoverStarted, StageTakeoverCompleted:
		return true
	case StageAssignmentSwept, StageViewPublished, StageViewSession, StageViewInstalled:
		// Once per term, or once per Worker per connection: rare, and the
		// only account there is of what happened. Never budgeted away.
		return true
	case StageSourceWithheld:
		// Outside the repeated-line budget, and it has to be. That budget is
		// per (reason, query group), and a withheld source has no query group -
		// it never became a Plan - so every line of a round would share one
		// bucket and roughly half of a full compile would be merged into a
		// suppressed count. The half that vanished is the half someone is
		// looking for: these lines exist because a strategy that is not running
		// cannot be asked about any other way here.
		//
		// What keeps the volume bounded is upstream instead: after its first
		// round a process reports only the objects whose disposition changed,
		// so the steady state is no lines at all. The first round of each
		// process is one burst, measured at 1,249 lines on this deployment
		// and about 48 times that on the largest, once per leader election -
		// and that burst is itself capped, with what did not fit counted
		// rather than dropped in silence. See WithheldLineBudget.
		return true
	default:
		return false
	}
}

func repeatedLogObservation(observation Observation) bool {
	switch observation.Stage {
	case StageOffsetGap, StageResourceSoft, StageResourceHard, StageResourceResumed, StageRestartRecovered:
		return true
	default:
		return isPhaseTwoWorkflowStage(observation.Stage) || observation.Result == ResultResumed || exceptionalLogResult(observation.Result)
	}
}

func isPhaseTwoWorkflowStage(stage Stage) bool {
	for _, pair := range phaseTwoComponentStages {
		if pair.Stage == stage {
			return true
		}
	}
	return false
}

func exceptionalLogResult(result Result) bool {
	switch result {
	case ResultTerminal, ResultRetrying, ResultPaused, Result(ResultTimeout), Result(ResultFailed):
		return true
	default:
		return false
	}
}

var _ Observer = (*LoggingObserver)(nil)

type serializedLogWriter struct {
	mu   sync.Mutex
	next io.Writer
}

func (w *serializedLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.next.Write(p)
}

// appendHeldByAttributes writes what held the previous round as flat keys.
//
// Flat, like everything else on these lines: the renderer emits keys rather
// than objects, and a nested held_by is written but invisible to anything
// filtering the line. It first shipped nested inside one cohort's bundle and
// the lines that most needed it -- the sixty-second and slower Query Groups
// being skipped -- had no bundle and therefore no cause on them at all.
// appendSlotCompletionKind writes the completion the Slot reached, when it is
// a word this build publishes.
//
// Guarded rather than written through: the line carries a closed vocabulary
// everywhere else, and a value nobody can enumerate turns a filterable key
// into free text.
func appendSlotCompletionKind(attributes []slog.Attr, kind string) []slog.Attr {
	if !ValidProgressCompletionKind(kind) {
		return attributes
	}
	return append(attributes, slog.String("completion_kind", kind))
}

func appendHeldByAttributes(attributes []slog.Attr, held *HeldByFacts) []slog.Attr {
	if held == nil {
		return attributes
	}
	attributes = append(attributes,
		slog.String("held_by", held.Decision),
		slog.Int64("held_by_at", held.AtUnixMilli),
	)
	switch held.Decision {
	case "query_cooldown":
		attributes = append(attributes,
			slog.Uint64("held_by_cooldown_failures", uint64(held.QueryCooldownFailures)),
			slog.Int64("held_by_cooldown_until", held.QueryCooldownUntilMilli),
		)
	case HeldByReadinessDeferred:
		attributes = append(attributes, slog.Int64("held_by_ready_at", held.ReadyAtUnixMilli))
	}
	return attributes
}

// sortedWireFormats is the batch's formats in one order, so two lines with
// the same counts read the same.
func sortedWireFormats(counts OutputWireFormatCounts) []string {
	formats := make([]string, 0, len(counts))
	for format := range counts {
		formats = append(formats, format)
	}
	sort.Strings(formats)
	return formats
}

// joinInt64 renders source times as one comma-separated value, so the
// minutes a window lacks are one key a search for the strategy finds rather
// than sixteen numbered ones.
func joinInt64(values []int64) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.FormatInt(value, 10)
	}
	return strings.Join(parts, ",")
}
