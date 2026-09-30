package observability

import "math"

// QueryTimingFacts describe frozen/query-attempt timing, not HTTP or Slot counts.
type QueryTimingFacts struct {
	IntervalSeconds              int64 `json:"interval_seconds"`
	CompletionOffsetSeconds      int64 `json:"completion_offset_seconds"`
	ReadyAtUnixMilli             int64 `json:"ready_at_unix_milli"`
	FrozenQueryDeadlineUnixMilli int64 `json:"frozen_query_deadline_unix_milli"`
	CompletionDeadlineUnixMilli  int64 `json:"completion_deadline_unix_milli"`
	QueryDeadlineUnixMilli       int64 `json:"query_deadline_unix_milli"`
	RecoveryBudgetMilli          int64 `json:"recovery_budget_milli,omitempty"`
}

// ShortPeriodCohorts are the evaluation intervals whose completion deadline is
// thirty seconds, which is what the cohort is actually about - a Slot that has
// to be finished within half a minute of the time it evaluates.
//
// 10 and 15 get there by carrying an explicit thirty-second offset, so their
// deadline is two to three intervals wide. 30 gets there by the offset
// defaulting to the interval, so its deadline is exactly one interval wide:
// the tightest of the three, and the one that was left out. Being left out was
// not a missing label - the producer, this normalizer and the runtime each
// wrote the pair "10s"/"15s" separately, so the set lived in three places and
// the archive of executions over thirty seconds could not be attributed. The
// set is declared once here for that reason.
var ShortPeriodCohorts = []string{"10s", "15s", "30s"}

// ShortPeriodCompletionOffsetSeconds is the deadline that defines the cohort.
const ShortPeriodCompletionOffsetSeconds = 30

// IsShortPeriodInterval reports whether an evaluation interval reaches that
// deadline. It is the numeric half of the same set: sites that decide by
// interval and sites that decide by cohort label have to agree about which
// Slots are short, and when they were written out separately they did not.
func IsShortPeriodInterval(interval int64) bool {
	return interval == 10 || interval == 15 || interval == 30
}

// IsShortPeriodCohort reports whether cohort is one of them.
func IsShortPeriodCohort(cohort string) bool {
	for _, known := range ShortPeriodCohorts {
		if cohort == known {
			return true
		}
	}
	return false
}

// ShortPeriodCompletionFacts exists only after an acknowledged Progress commit.
type ShortPeriodCompletionFacts struct {
	Cohort         string  `json:"cohort"`
	CompletionKind string  `json:"completion_kind"`
	LagSeconds     float64 `json:"lag_seconds"`
	// AttemptNo is which attempt at the Slot this completion was, from the
	// request that ran it. A lag over the deadline reads two ways -- the
	// first attempt was dispatched late, or an earlier attempt failed and
	// this one is the retry -- and the lag alone cannot tell them apart; the
	// live tail past fifteen seconds could not be attributed for want of it.
	// Zero is an emitter that did not say.
	AttemptNo uint32 `json:"attempt_no"`
}

// ShortPeriodCompletionKinds is every completion kind a short-period Slot's
// acknowledged commit may carry: the executed kinds and the two query-free
// closures. Declared once so the normalizer and the metric labels agree, and
// so the lag histogram can be told apart by kind: a GAP_SKIPPED closure's lag
// is how late the skip was booked, not how long an execution took, and mixed
// into one histogram the two made the 15s and 30s cohorts' p99 a statistic
// of skips.
var ShortPeriodCompletionKinds = []string{
	"FULL_COMPLETED", "FULL_EMPTY_COMPLETED", "COMPLETED_WITH_PARTIAL_GAP", "COMPLETED_WITH_UNAVAILABLE",
	"COMPLETED_WITH_TERMINAL", "GAP_SKIPPED", "SNAPSHOT_UNAVAILABLE",
}

// IsShortPeriodCompletionKind reports whether kind is one of them.
func IsShortPeriodCompletionKind(kind string) bool {
	for _, known := range ShortPeriodCompletionKinds {
		if kind == known {
			return true
		}
	}
	return false
}

func normalizeTimingFacts(o Observation) *QueryTimingFacts {
	f := o.QueryTiming
	if f == nil || o.Component != ComponentAccess || o.Stage != StageQueryBudgetResolved ||
		!IsShortPeriodInterval(f.IntervalSeconds) || f.CompletionOffsetSeconds != ShortPeriodCompletionOffsetSeconds ||
		f.ReadyAtUnixMilli <= 0 || f.FrozenQueryDeadlineUnixMilli <= 0 || f.CompletionDeadlineUnixMilli <= f.FrozenQueryDeadlineUnixMilli ||
		f.QueryDeadlineUnixMilli <= 0 || f.RecoveryBudgetMilli < 0 {
		return nil
	}
	copy := *f
	return &copy
}

func normalizeShortPeriodCompletion(o Observation) *ShortPeriodCompletionFacts {
	f := o.ShortPeriodCompletion
	if f == nil || o.Component != ComponentScheduler || o.Stage != StageSlotCompleted ||
		!IsShortPeriodCohort(f.Cohort) || f.LagSeconds < 0 || math.IsNaN(f.LagSeconds) || math.IsInf(f.LagSeconds, 0) || o.Err != nil {
		return nil
	}
	if !IsShortPeriodCompletionKind(f.CompletionKind) {
		return nil
	}
	switch o.Operation {
	case OperationNormal, OperationRetry, OperationReplay, OperationProbe:
	default:
		return nil
	}
	copy := *f
	return &copy
}
