// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package editclose

// The loop's own outcomes, beside the decisions: what a round, a strategy
// or an alert came to before or after it was decided. Closed, like the
// decisions: a metric label is made of them.
const (
	// OutcomeNotLeader is a round on a replica that is not the control
	// leader. Rounds.
	OutcomeNotLeader = "not_leader"
	// OutcomePublicationUnavailable is a round on a leader that holds no
	// publication of its own yet. Rounds.
	OutcomePublicationUnavailable = "publication_unavailable"
	// OutcomeRosterUnavailable is a round with no roster of the link's
	// strategies to walk: none read yet. Rounds.
	OutcomeRosterUnavailable = "roster_unavailable"

	// OutcomeStrategyUnpublished is a strategy the link lists and the
	// publication has no Plan of: the absent close's to decide. Strategies.
	OutcomeStrategyUnpublished = "strategy_unpublished"
	// OutcomePlanRetained is a strategy whose Plans are the last good ones
	// kept in place of a definition that did not compile: what runs is not
	// the current definition. Strategies.
	OutcomePlanRetained = "plan_retained"
	// OutcomePlansDisagree is a strategy whose Plans state different facts.
	// Strategies.
	OutcomePlansDisagree = "plans_disagree"
	// OutcomePlanUnrevisioned is a strategy whose Plans publish no snapshot
	// revision or no output identity: its alerts are not this close's.
	// Strategies.
	OutcomePlanUnrevisioned = "plan_unrevisioned"
	// OutcomeEvidenceUnavailable is a strategy whose active alerts the link
	// could not list this round. Strategies.
	OutcomeEvidenceUnavailable = "evidence_unavailable"
	// OutcomeEvidenceTruncated is a strategy whose listing the link answered
	// incomplete: the listed alerts are decided, the rest wait. Strategies.
	OutcomeEvidenceTruncated = "evidence_truncated"

	// OutcomeProducerForeign and OutcomeProducerUnknown are listed alerts of
	// another source, and ones naming none. Alerts.
	OutcomeProducerForeign = "producer_foreign"
	OutcomeProducerUnknown = "producer_unknown"
	// OutcomeRecordUnreadable is an alert whose record could not be read, or
	// was not this source's alert of this strategy: skipped, and read again
	// on a later round. Alerts.
	OutcomeRecordUnreadable = "record_unreadable"
	// OutcomeReadDeferred is a listed alert not read because the round's
	// record reads were spent: it is first in line next round. Alerts.
	OutcomeReadDeferred = "read_deferred"
	// OutcomeRemembered counts, among the decisions, the ones taken from
	// memory without a read: the alert was decided against the same facts
	// before. Alerts.
	OutcomeRemembered = "remembered"
	// OutcomeBusinessUnknown is a close decided for an alert whose record
	// names no business to file it under: not sent. Alerts.
	OutcomeBusinessUnknown = "business_unknown"
	// OutcomeCloseSent and OutcomeSendFailed are closes the producer
	// accepted and refused. Alerts.
	OutcomeCloseSent  = "close_sent"
	OutcomeSendFailed = "send_failed"
	// OutcomeMemoryFull counts the times the decision memory reached its
	// bound and was dropped whole: the next rounds read again what it held.
	OutcomeMemoryFull = "memory_full"
)

// Outcomes is every word the loop counts, decisions included, for the
// metric that pre-creates them.
var Outcomes = append(append(append([]string(nil), Decisions...), Notes...),
	OutcomeNotLeader, OutcomePublicationUnavailable, OutcomeRosterUnavailable,
	OutcomeStrategyUnpublished, OutcomePlanRetained, OutcomePlansDisagree, OutcomePlanUnrevisioned,
	OutcomeEvidenceUnavailable, OutcomeEvidenceTruncated,
	OutcomeProducerForeign, OutcomeProducerUnknown, OutcomeRecordUnreadable, OutcomeReadDeferred, OutcomeRemembered,
	OutcomeBusinessUnknown, OutcomeCloseSent, OutcomeSendFailed, OutcomeMemoryFull)

// Failures are the outcomes that could not read or could not send, which
// a log line names at warning.
var Failures = []string{OutcomeEvidenceUnavailable, OutcomeRecordUnreadable, OutcomeBusinessUnknown, OutcomeSendFailed,
	OutcomeMemoryFull, OutcomeFamilyUnknown, OutcomeRevisionUnknown}
