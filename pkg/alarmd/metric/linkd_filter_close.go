// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

func newLinkdFilterCloseCollector() *outcomeCounterCollector {
	return newOutcomeCounterCollector("linkd_filter_close_total",
		"The strategy closes alarmd runs through the alert link's search and close by filter (package filterclose), by "+
			"outcome. Per job (one decision's filter in one round): job_done, job_done_rederived (the filter ran out but "+
			"items came back certainly not closed: not settled, run again), job_incomplete (the round ended or the "+
			"dry run's count was spent), nothing_matched, expect_exceeded (the dry run matched more than the independent "+
			"expectation allows; nothing closed), filter_close_unsupported (501/404/405: nothing closes the decision), "+
			"link_busy (429), link_refused (another 4xx), link_failed (timeout, 5xx, transport), command_unavailable, "+
			"filter_refused, target_list_too_large (a scope close whose current targets do not fit one request: not "+
			"sent, never cut), page_lost (a real close page whose answer was lost: the candidates its dry run read just "+
			"before are kept to retry by id); where the job's expectation came from: expect_own_record (alarmd's own record of the episodes it "+
			"opened), expect_decided_ids; the record's: record_missing (no record, or none with a start: "+
			"decided alert by alert; the alerts opened before a record's start always are), record_truncated, record_unavailable (read failed: nothing decided), record_pruned (entries "+
			"removed after their decision found nothing), record_prune_failed. Per item: item_closed, "+
			"item_already_closed, item_ended_otherwise (409 invalid_transition), item_source_unpublished, "+
			"item_conflict_unknown, item_rederived (429, 504 not_attempted, 409 version_conflict: found again by the "+
			"filter), item_pending (5xx: kept to retry by id), item_pending_full, item_pending_lost, item_refused. Per "+
			"retry of a kept item: pending_closed, pending_already_closed, pending_ended_otherwise, "+
			"pending_left_decision (the decision's filter no longer names the alert: dropped, not closed), "+
			"pending_not_found, pending_retry_failed, pending_given_up, pending_refused, pending_unreadable, "+
			"pending_unavailable. The edit close's own: buckets_truncated, digest_mismatch, family_mismatch (a sample whose "+
			"no-data tag and evaluation_family disagree: no decision), sample_unreadable, "+
			"and edit_<word> for each edit decision and note - edit_revision_incomparable, beside its decision, an "+
			"alert whose strategy_version is of the other revision scheme than the current Plans' (a microsecond time "+
			"against a count), judged from its own record; alerts_read, alerts_truncated, alert_decided_close are the "+
			"edit close's and the alert-by-alert close's of a strategy with no record; absent_truncated (a tenant's "+
			"discovery stopped at its per-round bound). "+
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
			"write_failed (a change in a pipeline the Redis did not take). Counted per change; every cell exists from "+
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
