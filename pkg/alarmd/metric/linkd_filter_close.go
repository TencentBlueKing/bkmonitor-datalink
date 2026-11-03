// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
)

func newLinkdFilterCloseCollector() *outcomeCounterCollector {
	return newOutcomeCounterCollector("linkd_filter_close_total",
		"The strategy closes alarmd runs through the alert link's search and close by filter (package filterclose), by "+
			"outcome. Per job (one decision's filter in one round): job_done, job_done_refused (the filter ran out with items the link refused and left open - 400, 403, 409 "+
			"source_unpublished, item_conflict_unknown, item_refused: not settled, the record kept), job_done_rederived (the filter ran out but "+
			"items came back certainly not closed: not settled, run again), job_incomplete (the round ended or the "+
			"dry run's count was spent), nothing_matched, expect_exceeded (the dry run matched more than the independent "+
			"expectation allows; nothing closed), filter_close_unsupported (501/404/405: nothing closes the decision), "+
			"link_busy (429), link_refused (another 4xx), link_failed (timeout, 5xx, transport), link_answer_too_large (a 200 "+
			"past the client's bound: a dry run of more ids than it reads; nothing decided), command_unavailable, "+
			"filter_refused, target_list_too_large (a scope close whose current targets do not fit one request: not "+
			"sent, never cut), page_lost (a real close request whose answer was lost: the ids it was narrowed to, read "+
			"by its dry run just before, are kept to retry by id); where the job's expectation came from: expect_own_record (alarmd's own record of the episodes it "+
			"opened), expect_decided_ids; the record's: record_missing (no record, or none with a start: "+
			"decided alert by alert; the alerts opened before a record's start always are), record_truncated, record_unavailable (read failed: nothing decided), record_pruned (entries "+
			"removed after their decision found nothing), record_prune_failed. Per item: item_closed, "+
			"item_already_closed, item_ended_otherwise (409 invalid_transition), item_source_unpublished, "+
			"item_conflict_unknown, item_rederived (429, 504 not_attempted, 409 version_conflict: found again by the "+
			"filter), item_unavailable (503: the link refused before the close ran; found again next round), "+
			"item_not_found (404: enumerated, then gone before its close), item_invalid_command (400) and "+
			"item_tenant_mismatch (403): refused, not retried, "+
			"item_pending (another 5xx: kept to retry by id), item_pending_full, item_pending_lost, item_refused. Per "+
			"retry of a kept item: pending_closed, pending_already_closed, pending_ended_otherwise, "+
			"pending_left_decision (the decision's filter no longer names the alert: dropped, not closed), "+
			"pending_not_found, pending_retry_failed, pending_given_up, pending_refused, pending_unreadable, "+
			"pending_unavailable. The edit close's own: buckets_truncated, digest_mismatch, family_mismatch (a sample whose "+
			"no-data tag and evaluation_family disagree: no decision), sample_unreadable, "+
			"and edit_<word> for each edit decision and note - edit_revision_incomparable, beside its decision, an "+
			"alert whose strategy_version is of the other revision scheme than the current Plans' (a microsecond time "+
			"against a count), judged from its own record; alerts_read, alerts_truncated, alert_decided_close are the "+
			"edit close's and the alert-by-alert close's of a strategy with no record; absent_truncated (a tenant's "+
			"discovery stopped at its per-round bound). The filtered close takes no page: a job sends requests while there "+
			"is more, each a dry run of the same filter and then a real close narrowed to the ids that dry run read, so a "+
			"job never closes more than its expectation allows; close_cap_reached is a job stopped because its next dry run "+
			"matched more than its closes so far left of the expectation; "+
			"round_spent is a job ended before its next request because the round had less than one request's budget left; "+
			"link_query_failed is a 200 whose later candidate query failed (error_code query_failed), link_error_unknown a "+
			"200 with a top-level error code alarmd does not know; job_again counts a job run again within its round "+
			"because it was not settled and its last run closed something. "+
			"Every cell exists from the start so a zero is a reading and not an absence.")
}

// SetLinkdFilterCloseSource binds the filter close's counts. Safe before or
// after registration; a nil recorder is a no-op.
func (r *Recorder) SetLinkdFilterCloseSource(words []string, outcomes func() map[string]uint64) {
	if r == nil || r.phaseTwo.linkdFilterClose == nil {
		return
	}
	r.phaseTwo.linkdFilterClose.set(words, outcomes)
}

func newOpenEpisodeRecordCollector() *outcomeCounterCollector {
	return newOutcomeCounterCollector("open_episode_record_total",
		"Writes to alarmd's own record of the alert episodes it opened (package openepisodes), the expectation the "+
			"strategy-wide closes check a dry run against, by outcome: opened (an entry written for an ABNORMAL the "+
			"process had no own send of), ended (an entry removed for a RECOVERY), refreshed (a hash's life set again "+
			"for a strategy that only repeats its ABNORMALs), queue_full (a change dropped: the ACK path never waits), "+
			"write_failed (a change in a pipeline the Redis did not take), reensured (an entry written again, "+
			"set-if-absent, for an episode the process had an own send of, at most once an hour per series: it "+
			"restores an entry a close pruned or a lost write left out), ensured_full (a series not remembered "+
			"because the writer remembers its bound already: written only on the copy's word). Counted per change; every cell exists from "+
			"the start.")
}

// SetOpenEpisodeRecordSource binds the record writer's counts. Safe before
// or after registration; a nil recorder is a no-op.
func (r *Recorder) SetOpenEpisodeRecordSource(words []string, outcomes func() map[string]uint64) {
	if r == nil || r.phaseTwo.openEpisodeRecord == nil {
		return
	}
	r.phaseTwo.openEpisodeRecord.set(words, outcomes)
}

// SetNewOpensSource binds the per-strategy open count's outcomes. Safe
// before or after registration; a nil recorder is a no-op.
func (r *Recorder) SetNewOpensSource(words []string, outcomes func() map[string]uint64) {
	if r == nil || r.phaseTwo.newOpens == nil {
		return
	}
	r.phaseTwo.newOpens.set(words, outcomes)
}

func newNewOpensCollector() *outcomeCounterCollector {
	return newOutcomeCounterCollector("new_opens_total",
		"Alerts this process opened (an ABNORMAL the open-alert copy says is its episode's first, standard protocol "+
			"only), as the per-strategy hourly count behind the fleet's NEW_ALERTS_BURST line hears them, by outcome: "+
			"counted, untracked (its strategy not counted: the count holds its bound of strategies already, so that "+
			"strategy cannot be named). Every cell exists from the start.")
}

func newStrategyScopeCloseCollector() *outcomeCounterCollector {
	return newOutcomeCounterCollector("strategy_scope_close_total",
		"The control leader's strategy scope close (on its start and on each change of a strategy's target scope, one "+
			"close by filter of the strategy's alerts outside its current targets), by outcome: not_leader, "+
			"publication_unavailable, snapshot_unavailable, link_unavailable (no link control plane or no target "+
			"resolver), plans_disagree (the strategy's Plans carry different target plans), resolution_incomplete (the "+
			"target did not resolve Complete with its exclusions: nothing closed), resolution_stale (the target resolved "+
			"from a snapshot kept past its refresh: nothing closed, not a read of the scope), member_unreadable, scope_empty (resolved "+
			"Complete with no member: nothing closed), scope_unconfirmed (a scope read once: it closes when the next "+
			"round reads the same), scope_unchanged, refused_backoff (a strategy whose close was refused past its "+
			"record waiting a few rounds before the next try), "+
			"round_full (past the round's strategies), close_run, scope_settled (the close's word ends the decision until "+
			"the scope changes). The close's own word is in linkd_filter_close_total. Every cell exists from the start.")
}

// SetStrategyScopeCloseSource binds the strategy scope close's counts.
// Safe before or after registration; a nil recorder is a no-op.
func (r *Recorder) SetStrategyScopeCloseSource(words []string, outcomes func() map[string]uint64) {
	if r == nil || r.phaseTwo.strategyScopeClose == nil {
		return
	}
	r.phaseTwo.strategyScopeClose.set(words, outcomes)
}

// linkdFilterCloseRequestBuckets span the link's filtered close: a small
// dry run answers in tens of milliseconds, and any request runs up to the
// link's own 20 s budget (alert-close.md) or until the client gives up.
var linkdFilterCloseRequestBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 15, 20, 30}

func newLinkdFilterCloseRequests() *prometheus.HistogramVec {
	histogram := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Subsystem: metricSubsystem, Name: "linkd_filter_close_request_seconds",
		Help: "How long one request of the strategy closes' filtered close (package filterclose) to the alert link " +
			"took, from sent to answered or given up, by mode (dry_run: the count and the candidates before a close; " +
			"close: the real close of the ids its dry run read, as many as the link's own budget allows) and result. Not answered: timeout (the request's own budget ran out: the " +
			"closes the link made before stay saved), canceled (the round ended first), busy (429), unavailable (503), " +
			"http_4xx, http_5xx, transport (no status: the link was not reached or did not answer); invalid_response (a 200 " +
			"whose body could not be read - it broke off or did not decode, and the link may have acted on " +
			"it - or a request alarmd could not build and did not send: its command not a JSON object, its query " +
			"invalid, its body past the link's bound); answer_too_large (a 200 past the client's bound). Answered with a top-level error code: query_failed (a later " +
			"candidate query failed; the items read are answered), error_unknown (a code alarmd does not know). " +
			"Answered: ok (no item failed; every dry run); a real close " +
			"whose items all failed, by their one status, items_busy (429 or 504 not_attempted), items_unavailable " +
			"(503), items_5xx (another 5xx), items_failed (mixed or another); items_partial when only some failed. " +
			"Every cell exists from the start.",
		Buckets: append([]float64(nil), linkdFilterCloseRequestBuckets...),
	}, []string{"mode", "result"})
	for _, mode := range []string{"dry_run", "close"} {
		for _, result := range filterclose.RequestResults {
			histogram.WithLabelValues(mode, result)
		}
	}
	return histogram
}

// ObserveLinkdFilterCloseRequest observes one filtered close request. A
// result outside filterclose.RequestResults is not observed.
func (r *Recorder) ObserveLinkdFilterCloseRequest(dryRun bool, result string, elapsed time.Duration) {
	if r == nil || r.phaseTwo.linkdFilterCloseRequests == nil {
		return
	}
	known := false
	for _, word := range filterclose.RequestResults {
		known = known || word == result
	}
	if !known {
		return
	}
	mode := "close"
	if dryRun {
		mode = "dry_run"
	}
	r.phaseTwo.linkdFilterCloseRequests.WithLabelValues(mode, result).Observe(max(0, elapsed.Seconds()))
}
