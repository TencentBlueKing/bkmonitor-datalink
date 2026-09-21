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
	}
	if f := observation.CapacityRejection; f != nil {
		attributes = append(attributes, slog.String("capacity_phase", f.Phase), slog.Uint64("capacity_shared_used", f.SharedUsed), slog.Uint64("capacity_requested", f.Requested), slog.Uint64("capacity_limit", f.Limit))
		if f.OwnUsed != nil {
			attributes = append(attributes, slog.Uint64("capacity_own_used", *f.OwnUsed))
		}
	}
	if observation.CapacityBudget != "" {
		attributes = append(attributes, slog.String("capacity_budget", string(observation.CapacityBudget)))
	}
	if observation.SourceKind != "" {
		attributes = append(attributes, slog.String("source_kind", string(observation.SourceKind)))
	}
	attributes = appendObservationCounts(attributes, observation.Counts)
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
	if facts := observation.SegmentContent; facts != nil {
		attributes = append(attributes, slog.String("segment_content", facts.State))
	}
	if facts := observation.NoDataSlot; facts != nil {
		attributes = append(attributes,
			slog.String("no_data_outcome", facts.Outcome),
			slog.Int("no_data_outcome_plans", facts.Plans),
		)
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
	if observation.Err != nil {
		attributes = append(attributes,
			slog.String("error_type", fmt.Sprintf("%T", observation.Err)),
			slog.String("error", SanitizeErrorText(observation.Err.Error())),
		)
	}
	if admission.Suppressed > 0 {
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
		{"keys", counts.Keys}, {"state_bytes", counts.StateBytes},
	}
	for _, value := range values {
		if value.value > 0 {
			attributes = append(attributes, slog.Int64(value.name, value.value))
		}
	}
	return attributes
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
