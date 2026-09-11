package observability

const (
	StageRunnerReturned       = "runner_returned"
	StageDispatcherSnapshot   = "dispatcher_snapshot"
	StageQueryPermitWait      = "query_permit_wait"
	StageExpiredRangeReturned = "expired_range_returned"
)

type ExpiredRangeFacts struct {
	ReasonCode     ReasonCode
	Result         string
	CommittedSlots uint32
}

type DispatcherFacts struct {
	Active, Ready, Delayed int
	QueuesKnown            bool
}
type PermitWaitFacts struct{ Recovery bool }

func ValidRunOutcome(value string) bool {
	switch value {
	case "query_cooldown", "single_flight_busy", "ownership_rejected", "source_backoff", "source_retry", "source_blocked", "source_not_due", "source_error", "operation_not_ready", "admission_denied", "execute_returned", "cancelled", "panic", "other_error":
		return true
	}
	return false
}
func ValidExecuteOutcome(value string) bool {
	switch value {
	case "completed", "readiness_deferred", "retrying", "cancelled", "error", "incomplete":
		return true
	}
	return false
}
func ValidProgressCompletionKind(value string) bool {
	switch value {
	case "FULL_COMPLETED", "FULL_EMPTY_COMPLETED", "COMPLETED_WITH_PARTIAL_GAP", "COMPLETED_WITH_UNAVAILABLE", "COMPLETED_WITH_TERMINAL", "GAP_SKIPPED", "SNAPSHOT_UNAVAILABLE":
		return true
	}
	return false
}
