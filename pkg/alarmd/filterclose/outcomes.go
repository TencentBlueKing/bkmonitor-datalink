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
	// not_attempted, 409 version_conflict; item_unavailable: 503): not settled, the caller runs
	// the decision again until it answers job_done or nothing_matched.
	OutcomeJobDoneRederived = "job_done_rederived"
	// OutcomeJobDoneRefused is a job whose filter ran out with no item left
	// to find again, but some refused by the link and still open (400
	// invalid_command, 403 tenant_mismatch, 409 source_unpublished, a 409
	// of a code alarmd does not know, any other status): not
	// settled, so the record keeps their entries; not run again in the
	// round, since the same command would be refused.
	OutcomeJobDoneRefused = "job_done_refused"
	// OutcomeJobIncomplete is a job the round ended, or whose closes reached
	// the count its dry run checked, before the filter ran out - or whose
	// request got no further (nothing closed, no id newly refused), or whose
	// dry run had more past ids all refused earlier in the run; the next
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
	OutcomeLinkFailed = "link_failed"
	// OutcomeLinkAnswerTooLarge is a request answered 200 past the
	// client's bound (linkdmanage.ErrAnswerTooLarge): a dry run of more
	// ids than the bound holds, which a filter matching far past its
	// expectation answers again every round. No decision; a real close's
	// ids are kept as for a lost answer.
	OutcomeLinkAnswerTooLarge = "link_answer_too_large"
	OutcomeCommandUnavailable = "command_unavailable"
	OutcomeFilterRefused      = "filter_refused"
	// OutcomeTargetListTooLarge is a scope close whose current targets do
	// not fit one filter (ScopeFilter): nothing is sent, no part of it.
	OutcomeTargetListTooLarge = "target_list_too_large"
	// OutcomePageLost is a real close request whose whole answer was lost
	// (timeout, reset, 5xx): its candidates, read by the dry run sent just
	// before it, are kept to be retried by id (item_pending each). The word
	// keeps its name from when a request sent a page.
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
	// OutcomeItemUnavailable is a 503: the link refused before the close
	// ran (its close resources not configured). Certainly not closed and not
	// kept to retry by id; like item_rederived it leaves the job not
	// settled, and the next round's filter finds the alert again.
	OutcomeItemUnavailable = "item_unavailable"
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
	OutcomeAlertsRead      = "alerts_read"
	OutcomeAlertsTruncated = "alerts_truncated"
	// OutcomeWalkRemembered is a bucket the edit close did not read alert by
	// alert again: a whole pass of it ended under the same Plans' key with
	// nothing left it could not close, so every alert still in it decides
	// as it did. Before it a bucket of alerts that never end (an old
	// revision's, kept open by the stale-alerts ruling) was read and every
	// alert counted again each round.
	OutcomeWalkRemembered = "alerts_walk_remembered"
	// OutcomeWalkReopened is a remembered bucket walked again because an
	// alert opened in it after the newest one its pass read: a Slot frozen
	// with the older Plans, replayed or taken over after the edit, opens at
	// the older version, and has to be decided too.
	OutcomeWalkReopened      = "alerts_walk_reopened"
	OutcomeAlertDecidedClose = "alert_decided_close"
	// OutcomeAbsentTruncated is a tenant's discovery that found
	// MaxAbsentPerRound strategies and stopped; the next round goes on.
	OutcomeAbsentTruncated = "absent_truncated"
	// OutcomeCloseCapReached is a job whose closes so far leave less than
	// its next dry run matched: no further request this run, the job
	// incomplete; the next round checks again. A request closes only ids
	// its dry run read, so the job's closes stay within what is allowed.
	OutcomeCloseCapReached = "close_cap_reached"
	// OutcomeRoundSpent is a job ended before its next request because the
	// round had less than one request's budget left (RequestBudget):
	// nothing more sent, nothing more kept; the job incomplete.
	OutcomeRoundSpent = "round_spent"
	// OutcomeLinkQueryFailed is a 200 whose later candidate query failed:
	// the items read before are answered and has_more is set
	// (error_code query_failed). OutcomeLinkErrorUnknown is a 200 with a
	// top-level error code alarmd does not know.
	OutcomeLinkQueryFailed  = "link_query_failed"
	OutcomeLinkErrorUnknown = "link_error_unknown"
	// OutcomeItemNotFound is an item the link enumerated and then did not
	// find to close (404): gone, not closed by this command, nothing to
	// retry.
	OutcomeItemNotFound = "item_not_found"
	// OutcomeItemInvalidCommand (400 invalid_command) and
	// OutcomeItemTenantMismatch (403 tenant_mismatch) are items the link
	// refused the command for: not closed, and the same command would be
	// refused again, so not retried. item_refused is any other 4xx.
	OutcomeItemInvalidCommand = "item_invalid_command"
	OutcomeItemTenantMismatch = "item_tenant_mismatch"
	// OutcomeJobAgain is a job run again within its round because it has
	// more and its last run closed something (UntilProgressStops).
	OutcomeJobAgain = "job_again"
)

// Next is what the caller of a job does after its word (NextAfter): every
// caller that runs a job again on its own schedule decides from it, so a
// job the same command would answer the same way is not sent again at that
// schedule's pace.
type Next string

const (
	// NextSettled: the decision is done (job_done, nothing_matched).
	NextSettled Next = "settled"
	// NextAgain: the job got somewhere and has more - items to be found
	// again (job_done_rederived), the round's or a request's time spent,
	// the expectation's cap reached, a search page left (job_incomplete) -
	// and the next run goes on from a new dry run.
	NextAgain Next = "again"
	// NextTransient: a failure that may clear by itself - the link busy
	// (link_busy, 429) or not answering (link_failed: timeout, 5xx,
	// transport, a lost answer whose ids are kept to retry by id), or
	// alarmd's own Redis read (record_unavailable, command_unavailable).
	NextTransient Next = "transient"
	// NextHeld: the same command is answered the same way until something
	// outside the job changes - items refused (job_done_refused), the
	// request refused (link_refused), the dry run past the expectation
	// (expect_exceeded) or the client's bound (link_answer_too_large), a
	// filter alarmd cannot build (filter_refused, target_list_too_large),
	// filtered close unsupported (filter_close_unsupported) - and any word
	// not named here.
	NextHeld Next = "held"
)

// NextAfter is the class of a job's word. No word ("") is no job run - a
// caller's own refusal before it (an inactive close too near its
// boundary, an edit close that found nothing to close) - and is not a
// word of this classification: it reads held, so a caller must check for
// it first and not wait on a job that never ran.
func NextAfter(word string) Next {
	switch word {
	case OutcomeJobDone, OutcomeNothingMatched:
		return NextSettled
	case OutcomeJobDoneRederived, OutcomeJobIncomplete:
		return NextAgain
	case OutcomeLinkBusy, OutcomeLinkFailed, OutcomeRecordUnavailable, OutcomeCommandUnavailable:
		return NextTransient
	}
	return NextHeld
}

// Outcomes is every outcome, the edit decisions' words among them.
var Outcomes = append([]string{
	OutcomeJobDone, OutcomeJobDoneRederived, OutcomeJobDoneRefused, OutcomeJobIncomplete, OutcomeNothingMatched, OutcomeExpectExceeded, OutcomeUnsupported, OutcomeLinkBusy,
	OutcomeLinkRefused, OutcomeLinkFailed, OutcomeLinkAnswerTooLarge, OutcomeCommandUnavailable, OutcomeFilterRefused, OutcomeTargetListTooLarge,
	OutcomePageLost, OutcomeItemClosed, OutcomeItemAlreadyClosed, OutcomeItemEndedOtherwise, OutcomeItemSourceUnpublished, OutcomeItemConflictUnknown,
	OutcomeItemRederived, OutcomeItemUnavailable, OutcomeItemPending, OutcomeItemPendingFull, OutcomeItemPendingLost, OutcomeItemRefused,
	OutcomePendingClosed, OutcomePendingAlreadyClosed, OutcomePendingEndedOther, OutcomePendingLeftDecision, OutcomePendingNotFound,
	OutcomePendingRetryFailed, OutcomePendingGivenUp, OutcomePendingRefused, OutcomePendingUnreadable, OutcomePendingUnavailable,
	OutcomeExpectDecidedIDs, OutcomeExpectOwnRecord,
	OutcomeRecordMissing, OutcomeRecordTruncated, OutcomeRecordUnavailable, OutcomeRecordPruned, OutcomeRecordPruneFailed,
	OutcomeBucketsTruncated, OutcomeDigestMismatch, OutcomeFamilyMismatch, OutcomeSampleUnreadable, OutcomeAlertsRead, OutcomeAlertsTruncated,
	OutcomeAlertDecidedClose, OutcomeAbsentTruncated, OutcomeWalkRemembered, OutcomeWalkReopened,
	OutcomeCloseCapReached, OutcomeRoundSpent, OutcomeLinkQueryFailed, OutcomeLinkErrorUnknown, OutcomeItemNotFound, OutcomeItemInvalidCommand, OutcomeItemTenantMismatch, OutcomeJobAgain,
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
