// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

const (
	StageRunnerReturned       = "runner_returned"
	StageDispatcherSnapshot   = "dispatcher_snapshot"
	StageQueryPermitWait      = "query_permit_wait"
	StageExpiredRangeReturned = "expired_range_returned"
	StageDispatchTurnaway     = "dispatch_turnaway"
)

// DispatchTurnawayFacts is what the dispatcher knew at the moment it turned a
// short-period Query Group away from a full queue: which predicate decided,
// and the two values it compared. dispatch_queue_turnaways_total says how
// often a cohort is turned away and nothing about why; a 10s cohort still
// being turned away after the deadline order was corrected could be an
// arrival whose deadline is unknown, a queue tail that really does expire
// earlier, or a tie lost on order, and the counter reads the same for all
// three. Written only for short-period cohorts: the minute cohort is turned
// away at every tide by design and would drown the lines that answer a
// question.
type DispatchTurnawayFacts struct {
	// Outcome is the same closed set as dispatch_queue_turnaways_total.
	Outcome string `json:"outcome"`
	Cohort  string `json:"cohort"`
	// Verdict names the predicate that decided:
	// deadline_unknown - the arrival had no deadline, and an unknown deadline
	//   never displaces a known one;
	// tail_earlier - the queue's latest entry expires before the arrival;
	// tail_equal - same deadline, the tie went to the tail on ready time or
	//   arrival order;
	// displaced - the turned-away entry was the queue's latest and an arrival
	//   with an earlier deadline took its place;
	// ready_at_not_before / ready_at_displaced - the recovery queue's own
	//   order, which is by ready time.
	Verdict string `json:"verdict"`
	// DeadlineUnixMilli and ReadyAtUnixMilli are the turned-away Query Group's
	// own; zero when unknown.
	DeadlineUnixMilli int64 `json:"deadline_ms"`
	ReadyAtUnixMilli  int64 `json:"ready_at_ms"`
	// Kept* describe the Query Group that kept or took the place: the queue
	// tail the arrival could not beat, or the arrival that displaced the tail.
	KeptQueryGroup        string `json:"kept_query_group"`
	KeptCohort            string `json:"kept_cohort"`
	KeptDeadlineUnixMilli int64  `json:"kept_deadline_ms"`
	KeptReadyAtUnixMilli  int64  `json:"kept_ready_at_ms"`
	QueueLength           int    `json:"queue_length"`
	QueueCapacity         int    `json:"queue_capacity"`
}

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

// RunOutcomes is every word one Runner round can end on. It is the list
// ValidRunOutcome answers from, so a word added to one is added to both, and
// it is exported because held_by reports the same vocabulary: the two are read
// together and a second copy would be a second thing to keep in step.
var RunOutcomes = []string{
	"query_cooldown", "single_flight_busy", "ownership_rejected", "source_backoff", "source_retry",
	"source_blocked", "source_not_due", "source_error", "operation_not_ready", "admission_denied",
	"execute_returned", "cancelled", "panic", "other_error",
}

func ValidRunOutcome(value string) bool {
	for _, known := range RunOutcomes {
		if value == known {
			return true
		}
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
