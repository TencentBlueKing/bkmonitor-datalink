// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filterclose

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/editclose"

// The outcomes, closed: a metric label is made of them. A job is one
// decision's filter run in one round; an item is one alert's result in a
// filtered close; a pending outcome is one retry by id.
const (
	// The job's word. OutcomeJobDone is a job whose filter ran out with
	// every item it closed settled.
	OutcomeJobDone = "job_done"
	// OutcomeJobDoneRederived is a job whose filter ran out but some of its
	// items came back certainly not closed (item_rederived: 429, 504
	// not_attempted, 409 version_conflict): not settled, the caller runs
	// the decision again until it answers job_done or nothing_matched.
	OutcomeJobDoneRederived = "job_done_rederived"
	// OutcomeJobIncomplete is a job the round ended, or whose closes reached
	// the count its dry run checked, before the filter ran out; the next
	// round carries on from a new dry run.
	OutcomeJobIncomplete  = "job_incomplete"
	OutcomeNothingMatched = "nothing_matched"
	// OutcomeExpectExceeded is a job refused: its dry run matched more than
	// the independent expectation allows. Nothing was closed.
	OutcomeExpectExceeded = "expect_exceeded"
	// OutcomeUnsupported is filtered close unsupported: the link answered
	// 501, 404 or 405 to the search or the filtered close - its storage
	// cannot search (MySQL), or it has no such routes - or did within
	// unsupportedRecheck, and nothing was sent. Nothing closes the decision
	// instead: there is no other path.
	OutcomeUnsupported = "filter_close_unsupported"
	// OutcomeLinkBusy is a 429 of the whole request: no decision, next round.
	OutcomeLinkBusy = "link_busy"
	// OutcomeLinkRefused is a 4xx of the whole request other than 404, 405
	// and 429: the token (401), the DSL (400), the body (413). Nothing closed.
	OutcomeLinkRefused = "link_refused"
	// OutcomeLinkFailed is a request that failed, timed out, or answered
	// 5xx: no decision, next round.
	OutcomeLinkFailed         = "link_failed"
	OutcomeCommandUnavailable = "command_unavailable"
	OutcomeFilterRefused      = "filter_refused"
	// OutcomeTargetListTooLarge is a scope close whose current targets do
	// not fit one filter (ScopeFilter): nothing is sent, no part of it.
	OutcomeTargetListTooLarge = "target_list_too_large"
	// OutcomePageLost is a real close page whose whole answer was lost
	// (timeout, reset, 5xx): its candidates, read by the dry run sent just
	// before it, are kept to be retried by id (item_pending each).
	OutcomePageLost = "page_lost"

	// Each item of a filtered close.
	OutcomeItemClosed        = "item_closed"
	OutcomeItemAlreadyClosed = "item_already_closed"
	// OutcomeItemEndedOtherwise is a 409 invalid_transition: the alert ended
	// another way first (recovered, closed by another operation). Done.
	OutcomeItemEndedOtherwise    = "item_ended_otherwise"
	OutcomeItemSourceUnpublished = "item_source_unpublished"
	OutcomeItemConflictUnknown   = "item_conflict_unknown"
	// OutcomeItemRederived is an item certainly not closed - 429 busy, 504
	// not_attempted, 409 version_conflict - left for the decision's filter
	// to find again next round.
	OutcomeItemRederived = "item_rederived"
	// OutcomeItemPending is an item whose close may have been saved (5xx):
	// kept to be retried by id with its command.
	OutcomeItemPending          = "item_pending"
	OutcomeItemPendingFull      = "item_pending_full"
	OutcomeItemPendingLost      = "item_pending_lost"
	OutcomeItemRefused          = "item_refused"
	OutcomePendingClosed        = "pending_closed"
	OutcomePendingAlreadyClosed = "pending_already_closed"
	OutcomePendingEndedOther    = "pending_ended_otherwise"
	// OutcomePendingLeftDecision is a kept item whose alert the decision's
	// filter no longer names: dropped, not closed by a remembered id.
	OutcomePendingLeftDecision = "pending_left_decision"
	OutcomePendingNotFound     = "pending_not_found"
	OutcomePendingRetryFailed  = "pending_retry_failed"
	OutcomePendingGivenUp      = "pending_given_up"
	OutcomePendingRefused      = "pending_refused"
	OutcomePendingUnreadable   = "pending_unreadable"
	OutcomePendingUnavailable  = "pending_unavailable"

	// Where a job's expectation came from.
	OutcomeExpectDecidedIDs = "expect_" + ExpectDecidedIDs
	OutcomeExpectOwnRecord  = "expect_" + ExpectOwnRecord

	// The record's (ExpectOwnRecord). OutcomeRecordMissing is a strategy
	// with no record - its hash expired, or the Redis lost it - or one
	// without a start, whose close is then decided alert by alert (the
	// alerts opened before a record's start always are);
	// OutcomeRecordTruncated a record past openepisodes.MaxLoad, decided the
	// same way; OutcomeRecordUnavailable a read that failed: nothing is
	// decided, the next round reads again. OutcomeRecordPruned counts the
	// entries removed after a close of their decision found nothing at the
	// link; OutcomeRecordPruneFailed a removal that failed (the entries stay,
	// an over-count).
	OutcomeRecordMissing     = "record_missing"
	OutcomeRecordTruncated   = "record_truncated"
	OutcomeRecordUnavailable = "record_unavailable"
	OutcomeRecordPruned      = "record_pruned"
	OutcomeRecordPruneFailed = "record_prune_failed"

	// The edit close's own.
	OutcomeBucketsTruncated = "buckets_truncated"
	OutcomeDigestMismatch   = "digest_mismatch"
	// OutcomeFamilyMismatch is a bucket's sample whose no-data tag and
	// extra_data.evaluation_family disagree: no decision for the bucket.
	// A per-alert decision answers edit_family_unknown for such an alert.
	OutcomeFamilyMismatch   = "family_mismatch"
	OutcomeSampleUnreadable = "sample_unreadable"
	// OutcomeAlertsRead, OutcomeAlertsTruncated and OutcomeAlertDecidedClose
	// are also the alert-by-alert close's of a strategy with no record.
	OutcomeAlertsRead        = "alerts_read"
	OutcomeAlertsTruncated   = "alerts_truncated"
	OutcomeAlertDecidedClose = "alert_decided_close"
	// OutcomeAbsentTruncated is a tenant's discovery that found
	// MaxAbsentPerRound strategies and stopped; the next round goes on.
	OutcomeAbsentTruncated = "absent_truncated"
)

// Outcomes is every outcome, the edit decisions' words among them.
var Outcomes = append([]string{
	OutcomeJobDone, OutcomeJobDoneRederived, OutcomeJobIncomplete, OutcomeNothingMatched, OutcomeExpectExceeded, OutcomeUnsupported, OutcomeLinkBusy,
	OutcomeLinkRefused, OutcomeLinkFailed, OutcomeCommandUnavailable, OutcomeFilterRefused, OutcomeTargetListTooLarge,
	OutcomePageLost, OutcomeItemClosed, OutcomeItemAlreadyClosed, OutcomeItemEndedOtherwise, OutcomeItemSourceUnpublished, OutcomeItemConflictUnknown,
	OutcomeItemRederived, OutcomeItemPending, OutcomeItemPendingFull, OutcomeItemPendingLost, OutcomeItemRefused,
	OutcomePendingClosed, OutcomePendingAlreadyClosed, OutcomePendingEndedOther, OutcomePendingLeftDecision, OutcomePendingNotFound,
	OutcomePendingRetryFailed, OutcomePendingGivenUp, OutcomePendingRefused, OutcomePendingUnreadable, OutcomePendingUnavailable,
	OutcomeExpectDecidedIDs, OutcomeExpectOwnRecord,
	OutcomeRecordMissing, OutcomeRecordTruncated, OutcomeRecordUnavailable, OutcomeRecordPruned, OutcomeRecordPruneFailed,
	OutcomeBucketsTruncated, OutcomeDigestMismatch, OutcomeFamilyMismatch, OutcomeSampleUnreadable, OutcomeAlertsRead, OutcomeAlertsTruncated,
	OutcomeAlertDecidedClose, OutcomeAbsentTruncated,
}, editWords()...)

func editWords() []string {
	seen := map[string]bool{}
	var words []string
	for _, group := range [][]string{editclose.Decisions, editclose.Notes, editclose.GroupDecisions} {
		for _, word := range group {
			if word != "" && !seen[word] {
				seen[word] = true
				words = append(words, "edit_"+word)
			}
		}
	}
	return words
}
