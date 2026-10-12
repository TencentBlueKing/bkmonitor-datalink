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
	"log/slog"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// warnReasons are the reasons a result that is not a failure is written at
// WARN for: the ones the fleet view files under this deployment's own checks
// (a defect, a dependency down, detection abandoned, a timeline pruned) and
// under a backend not answering. Every other reason a strategy, the data or
// the platform owns, or that is a normal value, is INFO. The fleet package
// holds the test that keeps the two in step: a reason the view moves to
// another owner moves here too, or that test fails.
var warnReasons = makeReasonSet([]ReasonCode{
	"ACTIVATION_MISSING", "ACTIVATION_READ_FAILED", "AUDIT_DROP", "BACKEND_CAPABILITY_MISSING",
	"BLOCKED_EXACT_SET_UNAVAILABLE", "COMPLETION_OFFSET_BELOW_RESERVE", "CONTENT_SCOPE_MOVED", "SCHEDULE_UNREADABLE",
	"EXECUTION_BUDGET_EXHAUSTED", "GAP_APPLY_CONFLICT", "GAP_APPLY_STALE_VERSION",
	"GAP_GUARD_CONFLICT", "GAP_GUARD_DISAGREE", "GAP_SKIPPED", "GAP_WRITE_RETRYABLE",
	"KAFKA_UNAVAILABLE", "MESSAGE_BUDGET_EXCEEDED", "OUTPUT_ACK_UNKNOWN", "OUTPUT_CLIENT_REJECTED",
	"OUTPUT_CONVERSION_REJECTED", "OUTPUT_LEASE_EXPIRING", "OWNERSHIP_LEASE_BUSY",
	"OWNERSHIP_NOT_DESIRED", "OWNERSHIP_STALE_FENCE", "PROGRESS_BEGIN_FAILED",
	"PROGRESS_BEGIN_REJECTED", "PROVIDER_UNAVAILABLE", "QUERY_PARTIAL", "QUERY_TIMEOUT",
	"QUERY_UNAVAILABLE", "READINESS_BUDGET_INVALID", "RECORD_TOO_LARGE", "REDIS_UNAVAILABLE",
	"RESOURCE_HARD_STOP", "SCHEDULE_PRUNED", "SCHEDULE_REPAIRED", "SCHEDULE_REOPENED", "SLOT_BUDGET_EXCEEDED", "SLOT_SOURCE_RETRY",
	"SNAPSHOT_RETENTION_INSUFFICIENT", "SNAPSHOT_RETRY_PENDING", "SNAPSHOT_UNAVAILABLE",
	"STATE_BUDGET_EXCEEDED", "STATE_CORRUPT", "STATE_LEVEL_CONTRACT_MISMATCH", "STATE_READ_DEADLINE",
	"STATE_READ_TIMEOUT", "STATE_SCHEMA_UNSUPPORTED", "STATE_STALE_VERSION", "STATE_VERSION_CONFLICT",
	"STATE_WRITE_RETRYABLE", "TRIGGER_INVARIANT", "VALIDATION_BUDGET_EXCEEDED", "VIEW_NOT_EXECUTABLE",
	"view_not_executable", "QUERY_PERMIT_DEADLINE", "QUERY_PERMIT_QUEUE_FULL", "QUERY_PERMIT_RECOVERY_OFF",
	"QUERY_REASON_UNRECORDED",
	// A Plan's no-data detection that kept skipping until it counted as
	// stalled: once per stall, and the Plan is not detecting absence.
	"SKIPPED_QUERY_NOT_FULL", "SKIPPED_SLOT_BUDGET", "SKIPPED_MEMORY_UNREADABLE", "SKIPPED_HOSTS_UNRESOLVED",
	"SKIPPED_DERIVATION_FAILED", "SKIPPED_TARGET_SELECTOR_UNAVAILABLE", "SKIPPED_TARGET_MEMBERS_DROPPED",
	"SKIPPED_OUTPUT_FAILED", "SKIPPED_ANSWER_TRUNCATED",
	// A reason that could not be named is never quiet: the word for a site
	// that failed to report one, and the one an unlisted word folds into.
	ReasonInternalUnknown, ReasonNotReported, ReasonOther,
	// The contract's class words a site reports when it has no finer one: a
	// retryable or a deterministic failure of this deployment's own.
	ReasonContractRetryable, ReasonContractDeterministic,
})

// infoActivationKinds are the activation failure kinds that only mean the
// next round tries again: the set was still draining, or another writer won
// the compare-and-set. Every other kind is this deployment failing to
// activate, at WARN.
var infoActivationKinds = map[string]bool{"not_drained": true, "cas_conflict": true}

// warnViewStreamReasons are the view stream's words for a configuration or a
// defect -- a token, a protocol, a digest, a registry that does not answer.
// Its other words are a session's ordinary life: no leader yet, not the
// leader, a stream closed, replaced or idle.
var warnViewStreamReasons = makeReasonSet([]ReasonCode{
	"LISTENER_UNPARSABLE", "DELTA_BASE_MISMATCH", "DELTA_DIGEST_MISMATCH", "SNAPSHOT_INVALID",
	"SNAPSHOT_INCOMPLETE", "VIEW_FOR_ANOTHER_WORKER", "UNKNOWN_WORKER", "BAD_TOKEN", "PROTOCOL_VERSION",
	"HELLO_EXPECTED", "REGISTRY_UNAVAILABLE", "DISCOVERY_FAILED", "NO_ROUTE", "RECV_FAILED",
	"LEADER_NO_ENDPOINT", "LEADER_UNREGISTERED",
})

// warnDecisionReasons are the scheduler decisions that say it could not
// decide or could not do what it decided; the others are a replay or range
// decided by design.
var warnDecisionReasons = makeReasonSet([]ReasonCode{"unexplained", "freeze_failed", "proof_too_large"})

// warnMaintenanceReasons are effective-time maintenance outcomes that are a
// close or a plan failing; the others are a close acknowledged, contention
// or a wait.
var warnMaintenanceReasons = makeReasonSet([]ReasonCode{
	"close_precheck_failed", "close_send_failed", "maintenance_plan_uncompilable",
	"close_identity_invalid", "unavailable", "unsupported_runner",
})

// warnAbsentCloseReasons are absent-strategy close outcomes that could not
// read, could not send or ran out of memory; the others are a close made,
// deferred, within its grace, or refused by design.
var warnAbsentCloseReasons = makeReasonSet([]ReasonCode{
	"index_unreadable", "evidence_unavailable", "send_failed", "memory_full", "link_unavailable",
	"link_unhealthy", "snapshot_unusable", "snapshot_empty", "snapshot_stale",
})

// ReasonLogLevel is the level a result that is neither a success nor a
// failure is written at for its reason.
func ReasonLogLevel(reason ReasonCode) slog.Level {
	switch {
	case reason == ReasonNone:
		return slog.LevelInfo
	case inReasonSet(warnReasons, reason), inReasonSet(warnViewStreamReasons, reason), inReasonSet(warnDecisionReasons, reason),
		inReasonSet(warnMaintenanceReasons, reason), inReasonSet(warnAbsentCloseReasons, reason):
		return slog.LevelWarn
	case inReasonSet(activationFailureReasonSet, reason):
		if _, kind, ok := strings.Cut(string(reason), "/"); ok && infoActivationKinds[kind] {
			return slog.LevelInfo
		}
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// ownershipContentionReasons are the store's refusals of a handover: another
// replica still holds the lease, the Query Group is not this replica's by
// the assignment, or the fence moved. On a rollout each Query Group meets
// one of them once, by design -- on the stage that decides the handover, or
// on a Slot that was running when the Query Group moved.
var ownershipContentionReasons = makeReasonSet([]ReasonCode{
	contract.ReasonOwnershipNotDesired, contract.ReasonOwnershipLeaseBusy, contract.ReasonOwnershipStaleFence,
})

func inReasonSet(set map[ReasonCode]struct{}, reason ReasonCode) bool {
	_, in := set[reason]
	return in
}

// failedResult is a result written at ERROR unless it is a handover refusal.
func failedResult(result Result) bool {
	return result == Result(ResultFailed) || result == Result(ResultTimeout)
}

// routineResult is a success or a start: the line a workflow stage writes
// for every Slot of every Query Group, counted and not written.
func routineResult(result Result) bool {
	return result == Result(ResultSuccess) || result == Result(ResultStarted)
}

// observationLevel is the level a written line takes. A handover refusal is
// INFO wherever it is met, and so is a Slot cancelled from above; any other
// failure is ERROR; a result something
// else held is INFO, because the holder's own entry and exit are the lines
// that say why; a round refused while the view has not carried its Query
// Group yet is INFO, the wait every Worker starts with and every move
// brings; any other result takes its reason's level, and a success is INFO.
func observationLevel(observation Observation) slog.Level {
	if routineResult(observation.Result) {
		return slog.LevelInfo
	}
	if inReasonSet(ownershipContentionReasons, observation.ReasonCode) || observation.ReasonCode == ReasonSlotCancelled {
		return slog.LevelInfo
	}
	if failedResult(observation.Result) {
		return slog.LevelError
	}
	if observation.HeldBy != nil || observation.AwaitingView {
		return slog.LevelInfo
	}
	return ReasonLogLevel(observation.ReasonCode)
}

// routineStages are the steps every Slot of every Query Group passes through,
// and the ownership steps a handover repeats for each Query Group. Their
// success was nearly the whole log -- about 47 lines a Slot -- and every fact
// on it is in the stage's metrics, the Slot's record or diagnose; it is
// counted and not written. A workflow stage outside this list writes its
// success as an event (a turnaway, a cutover, a replay taken over), under the
// hourly sample.
var routineStages = map[Stage]bool{
	StageEvaluationCompleted: true, StageFrozenPlanGeneration: true, StageMutationCompared: true,
	StageScheduleDue: true, StageQueryAdmission: true, StageSlotStarted: true, StageSlotCompleted: true,
	StageSlotReadinessArrival: true, StageQueryBudgetResolved: true, StageQueryCompleted: true,
	StageSlotSourceCompleted: true, StageRunnerCompleted: true, StageGapLoaded: true, StageGapGuardCommitted: true,
	StageGapGuardProgress: true, StageNoDataDecided: true, StageNoDataMemoryRead: true, StageNoDataMemoryWritten: true,
	StageNoDataMemoryRenewed: true, StageProgressCommitted: true, StageStatePreflight: true, StageStateAdmission: true,
	StageStateApplied: true, StageFrozenStateRenewed: true, StageSideEffectAdmission: true, StageFenceChecked: true,
	StageEventACKed: true, StageTargetResolved: true,
	StageLeaseRenewed: true, StageAssignmentAcquired: true, StageAssignmentLost: true,
	StageTakeoverStarted: true, StageTakeoverCompleted: true,
}

// unwrittenStage is a stage that has lines counted and not written.
func unwrittenStage(stage Stage) bool { return routineStages[stage] || roundStages[stage] }

// roundStages are the Control Leader's per-round lines. Their success is
// written when the round changed something -- every one of those, since they
// are rare and each is a decision -- and counted and not written when it did
// not: the facts of an unchanged round are all in the round's metrics.
var roundStages = map[Stage]bool{
	StageRebalancePlanned: true, StageAssignmentIndexRead: true, StageAssignmentIndexWritten: true,
	StageSnapshotRefreshed: true, StageDrainingQGReconciled: true,
	StageControlReadsSpent: true, StageActiveQGSet: true, StageObjectCatalog: true,
}

// roundChanged says a Control Leader round line has something new on it: the
// round moved, rewrote or found missing something, read the source changed,
// or holds Query Groups draining. Three of the stages carry no facts at all
// on their line and never have anything new.
func roundChanged(observation Observation) bool {
	switch observation.Stage {
	case StageRebalancePlanned:
		facts := observation.Rebalance
		return facts != nil && (facts.PlannedMoves > 0 || facts.PublishedMoves > 0 || facts.Conflicts > 0 || facts.Paused)
	case StageAssignmentIndexRead:
		// A worker that reads just before the Leader writes sees the previous
		// round once: stale for one round is the ordinary race, the same
		// healthy value assignment_index_stale_rounds names. Two or more is an
		// index that stopped advancing.
		facts := observation.AssignmentIndex
		if facts == nil {
			return false
		}
		switch facts.Result {
		case AssignmentIndexFresh:
		case AssignmentIndexStale:
			if facts.StaleRounds >= 2 {
				return true
			}
		default:
			return true
		}
		return facts.Rewritten > 0 || facts.Missing > 0
	case StageAssignmentIndexWritten:
		facts := observation.AssignmentIndex
		return facts != nil && (facts.Result != "" || facts.Rewritten > 0 || facts.Missing > 0)
	case StageSnapshotRefreshed:
		return observation.SourceRefresh == nil || observation.SourceRefresh.Status != SourceRefreshUnchanged
	case StageDrainingQGReconciled:
		return observation.DrainingQG != nil && observation.DrainingQG.Total > 0
	default:
		return false
	}
}
