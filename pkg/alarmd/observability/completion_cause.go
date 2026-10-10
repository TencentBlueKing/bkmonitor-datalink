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
	"slices"
)

// ProgressCompletionCauses is every cause a committed Slot's completion can
// carry (execution.CompletionCause), closed: the words the cause counter and
// the completion line take. "NONE" is a completion that carried no cause.
var ProgressCompletionCauses = []string{"DATA_NOT_READY", "PLAN_UNAVAILABLE", "PRIMARY_INPUT_UNAVAILABLE",
	"LEVEL_OUTCOME_UNKNOWN", "GAP_GUARD_WARMING", "PRIMARY_INPUT_PARTIAL", "CONFIG_DRIFT", "PLAN_REACTIVATED",
	"PLAN_NOT_ACTIVE"}

// ProgressCompletionCauseNone is the cause word of a completion that carried
// none.
const ProgressCompletionCauseNone = "NONE"

// NormalizeProgressCompletionCause is a cause as the counter and the line
// write it: a known word, NONE for none, and other for a word outside the
// list.
func NormalizeProgressCompletionCause(cause string) string {
	switch {
	case cause == "":
		return ProgressCompletionCauseNone
	case slices.Contains(ProgressCompletionCauses, cause):
		return cause
	default:
		return string(ReasonOther)
	}
}

// CompletionAttributionReasons are the reasons a completion names for a
// primary input whose code was the fallback: the query was not sent, or no
// attempt said why (execution.ReasonQueryNotAttempted and
// ReasonQueryReasonUnrecorded). Named so the counter and the line keep them.
var CompletionAttributionReasons = []ReasonCode{"QUERY_NOT_ATTEMPTED", "QUERY_REASON_UNRECORDED"}

var completionAttributionReasonSet = makeReasonSet(CompletionAttributionReasons)

// NormalizedReasonCount is how many words NormalizeReason can return: its
// catalogues and the three words it answers with outside them. A counter
// labelled by a normalized reason has at most this many values of it.
func NormalizedReasonCount() int {
	return len(commonReasonSet) + len(resourceReasonSet) + len(contractObservationReasonSet) + len(activationFailureReasonSet) +
		len(viewStreamReasonSet) + len(schedulerDecisionReasonSet) + len(effectiveMaintenanceReasonSet) + len(absentCloseReasonSet) +
		len(completionAttributionReasonSet) + 3
}

// CompletionScopeFacts is where a committed Slot's completion cause was
// found: the Plan, and the Level for a Level's outcome or the physical query
// for the primary input. The cause and its reason say what kind of thing did
// not answer; this says which one did not.
type CompletionScopeFacts struct {
	TenantID      string
	BusinessID    string
	StrategyID    string
	LevelID       uint32
	HasLevel      bool
	PhysicalQuery string
}

// appendCompletionCause writes a committed Slot's completion cause, its
// reason and where it was found, when the completion carried a cause.
func appendCompletionCause(attributes []slog.Attr, observation Observation) []slog.Attr {
	if observation.Stage != StageProgressCommitted || observation.ProgressCompletionCause == "" {
		return attributes
	}
	attributes = append(attributes,
		slog.String("completion_cause", NormalizeProgressCompletionCause(observation.ProgressCompletionCause)),
		slog.String("completion_reason", string(NormalizeReason(ReasonCode(observation.ProgressCompletionReason), ResultDegraded))),
	)
	scope := observation.ProgressCompletionScope
	if scope == nil {
		return attributes
	}
	if scope.StrategyID != "" {
		attributes = append(attributes, slog.String("completion_strategy", scope.StrategyID),
			slog.String("completion_business", scope.BusinessID))
	}
	if scope.HasLevel {
		attributes = append(attributes, slog.Uint64("completion_level", uint64(scope.LevelID)))
	}
	if scope.PhysicalQuery != "" {
		attributes = append(attributes, slog.String("completion_query", scope.PhysicalQuery))
	}
	return attributes
}
