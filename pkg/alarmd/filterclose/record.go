// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filterclose

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// A strategy-wide close (absent, inactive, a scope close) is checked
// against alarmd's own record of the episodes it emitted for the strategy:
// the entries the decision covers are the expectation. Without a record
// the filter is not run as a close at all: each candidate alert the
// search pages is decided from its own labels and dimensions, and the
// ones decided are closed by their alert ids under the decision's filter.

// recordKey is the scope's record.
func recordKey(scope Scope) openepisodes.Key {
	return openepisodes.Key{TenantID: scope.TenantID, StrategyID: scope.StrategyID}
}

// readRecord is the strategy's record, or the word that ends the job:
// OutcomeRecordMissing (counted as missing or truncated) where it is to be
// decided alert by alert, OutcomeRecordUnavailable where nothing is.
func (closer *Closer) readRecord(ctx context.Context, scope Scope) (openepisodes.Record, string) {
	record, err := closer.record.Load(ctx, recordKey(scope))
	switch {
	case err != nil:
		closer.count(OutcomeRecordUnavailable, 1)
		return openepisodes.Record{}, OutcomeRecordUnavailable
	case record.Truncated:
		closer.count(OutcomeRecordTruncated, 1)
		return openepisodes.Record{}, OutcomeRecordMissing
	case !record.Exists:
		closer.count(OutcomeRecordMissing, 1)
		return openepisodes.Record{}, OutcomeRecordMissing
	}
	return record, ""
}

// covered is how many entries opened from vouch on the decision covers, an
// unreadable entry counted in, and every entry match selects - the ones a
// close that found nothing prunes. An entry opened before vouch is not
// counted: its alert may be older than the record (an episode still open
// when the record started, written again by its first ABNORMAL since), and
// the dry run it would count toward reads only alerts opened from vouch.
func covered(record openepisodes.Record, vouch time.Time, match func(openepisodes.Entry) bool) (int, []string) {
	n := 0
	var fingerprints []string
	for _, entry := range record.Entries {
		switch {
		case match(entry):
			if entry.OpenedAt >= vouch.Unix() {
				n++
			}
			fingerprints = append(fingerprints, entry.Fingerprint)
		case entry.Unreadable:
			n++
		}
	}
	return n, fingerprints
}

// recordSettle is the least time after its start a record vouches for
// what it holds. A record that starts while episodes are open (the
// rollout, a Redis loss) is written each such episode again by the first
// ABNORMAL of it a worker acknowledges after its start - its copy holds no
// own send of it - which arrives within the strategy's period; such an
// entry opens at the rewrite, its alert long before.
const recordSettle = time.Hour

// settle is how long after its start a record vouches for a strategy
// evaluated every period: two of its periods, so the rewrite of every
// episode still open at the start has arrived (the first ABNORMAL comes
// within one period, a round late or retried within the second), and
// never less than recordSettle. A period not known (zero: a strategy no
// longer published) settles at recordSettle; an episode of a longer
// period written again after it is counted once though its alert is
// outside the dry run, widening what is allowed by one as any over-count
// does.
func settle(period time.Duration) time.Duration {
	return max(recordSettle, 2*period)
}

// readSince is the moment from which the record vouches for the alerts
// the link opened (its start plus settle(period)), or the word that ends
// the job: OutcomeRecordMissing (counted) where the strategy has no record
// or one without a start, OutcomeRecordUnavailable where the read failed.
func (closer *Closer) readSince(ctx context.Context, scope Scope, period time.Duration) (time.Time, string) {
	since, ok, err := closer.record.Since(ctx, recordKey(scope))
	switch {
	case err != nil:
		closer.count(OutcomeRecordUnavailable, 1)
		return time.Time{}, OutcomeRecordUnavailable
	case !ok:
		closer.count(OutcomeRecordMissing, 1)
		return time.Time{}, OutcomeRecordMissing
	}
	return since.Add(settle(period)), ""
}

// recorded runs job over the alerts opened since the record's start
// against the record's entries match selects, decides the alerts opened
// before the start alert by alert with decide (the record cannot vouch for
// them: they opened before it was created), prunes the entries when the
// link holds none of the decision's alerts, and decides every alert one by
// one where the strategy has no record. It returns the first word that did
// not settle, else job_done when anything was closed, else
// nothing_matched.
func (closer *Closer) recorded(ctx context.Context, job Job, match func(openepisodes.Entry) bool,
	decide func(linkdmanage.Alert) bool) string {
	if word := closer.refuseUnsupported(); word != "" {
		return word
	}
	since, word := closer.readSince(ctx, job.Filter.scope, job.period)
	switch word {
	case OutcomeRecordMissing:
		return closer.perAlert(ctx, job, decide)
	case OutcomeRecordUnavailable:
		return word
	}
	recent, older := job, job
	if recent.Recheck.body == nil {
		recent.Recheck = job.Filter
	}
	older.Recheck = recent.Recheck
	var err, olderErr error
	recent.Filter, err = openedSince(job.Filter, since)
	older.Filter, olderErr = openedBefore(job.Filter, since)
	if err != nil || olderErr != nil {
		closer.count(OutcomeFilterRefused, 1)
		return OutcomeFilterRefused
	}
	var prune []string
	recent.expect = func(ctx context.Context) (Expectation, string) {
		record, word := closer.readRecord(ctx, job.Filter.scope)
		if word != "" {
			return Expectation{}, word
		}
		n, fingerprints := covered(record, since, match)
		prune = fingerprints
		return Expectation{Count: n, Source: ExpectOwnRecord}, ""
	}
	word = closer.Run(ctx, recent)
	if word == OutcomeRecordMissing {
		return closer.perAlert(ctx, job, decide)
	}
	if word == OutcomeUnsupported {
		return word
	}
	olderWord := closer.perAlert(ctx, older, decide)
	if word == OutcomeNothingMatched && olderWord == OutcomeNothingMatched {
		closer.prune(ctx, job.Filter.scope, prune)
	}
	return worse(word, olderWord)
}

// worse is the word of two parts of one decision: the first that did not
// settle, else job_done when either closed something, else
// nothing_matched.
func worse(a, b string) string {
	switch {
	case !settledWord(a):
		return a
	case !settledWord(b):
		return b
	case a == OutcomeJobDone || b == OutcomeJobDone:
		return OutcomeJobDone
	}
	return OutcomeNothingMatched
}

func settledWord(word string) bool { return word == OutcomeNothingMatched || word == OutcomeJobDone }

// prune removes the entries of a decision the link holds nothing of.
func (closer *Closer) prune(ctx context.Context, scope Scope, fingerprints []string) {
	if len(fingerprints) == 0 {
		return
	}
	if err := closer.record.Prune(ctx, recordKey(scope), fingerprints); err != nil {
		closer.count(OutcomeRecordPruneFailed, 1)
		return
	}
	closer.count(OutcomeRecordPruned, len(fingerprints))
}

// perAlert is a decision's close without a record: the decision's active
// alerts paged with the search, up to maxAlertsPerTurn a round from where
// the term's last round stopped, each decided from its own record, the
// ones decided closed by their ids under the decision's filter (so an
// alert that left the decision meanwhile is not closed). It returns the
// first word of a close that did not settle (anything but job_done and
// nothing_matched), else OutcomeJobIncomplete while the walk has pages
// left, OutcomeJobDone when a close ran, or OutcomeNothingMatched when
// nothing was decided.
func (closer *Closer) perAlert(ctx context.Context, job Job, decide func(linkdmanage.Alert) bool) string {
	read, err := activeOnly(job.Filter)
	if err != nil {
		closer.count(OutcomeFilterRefused, 1)
		return OutcomeFilterRefused
	}
	walkKey := "alerts:" + filterKey(job.Filter)
	closer.mu.Lock()
	after := closer.buckets[walkKey]
	closer.mu.Unlock()
	var ids []string
	n := 0
	for n < maxAlertsPerTurn {
		page, err := closer.link.Search(ctx, job.Filter.scope.TenantID, read.body, after, min(linkdmanage.MaxLimit, maxAlertsPerTurn-n))
		if err != nil {
			return closer.failed(err)
		}
		for _, alert := range page.Items {
			n++
			if alert.Status == "active" && decide(alert) {
				ids = append(ids, alert.AlertID)
			}
		}
		after = page.Next
		if page.Next == "" || len(page.Items) == 0 {
			after = ""
			break
		}
	}
	closer.count(OutcomeAlertsRead, n)
	if after != "" {
		closer.count(OutcomeAlertsTruncated, 1)
	}
	closer.mu.Lock()
	closer.buckets[walkKey] = after
	closer.mu.Unlock()
	closer.count(OutcomeAlertDecidedClose, len(ids))
	word := OutcomeNothingMatched
	recheck := job.Recheck
	if recheck.body == nil {
		recheck = job.Filter
	}
	for start := 0; start < len(ids); start += linkdmanage.MaxLimit {
		batch := ids[start:min(len(ids), start+linkdmanage.MaxLimit)]
		byID, err := narrowed(job.Filter, terms(fieldAlertID, sorted(batch)))
		if err != nil {
			closer.count(OutcomeFilterRefused, 1)
			if settledWord(word) {
				word = OutcomeFilterRefused
			}
			continue
		}
		batchWord := closer.Run(ctx, Job{Filter: byID, Decision: job.Decision, Version: job.Version, Digest: job.Digest, Revision: job.Revision,
			Batch:  job.Batch + job.Filter.batch,
			Expect: Expectation{Count: len(batch), Source: ExpectDecidedIDs}, Reason: job.Reason, Recheck: recheck})
		if batchWord == OutcomeUnsupported {
			return batchWord
		}
		switch {
		case !settledWord(batchWord) && settledWord(word), word == OutcomeNothingMatched:
			word = batchWord
		}
	}
	if after != "" && (word == OutcomeJobDone || word == OutcomeNothingMatched) {
		return OutcomeJobIncomplete
	}
	return word
}

// labelIs decides an alert by one label's value.
func labelIs(name, value string) func(linkdmanage.Alert) bool {
	return func(alert linkdmanage.Alert) bool {
		got, ok := alert.Label(name)
		return ok && got == value
	}
}

func everyEntry(openepisodes.Entry) bool { return true }

func versionIs(version string) func(openepisodes.Entry) bool {
	return func(entry openepisodes.Entry) bool { return entry.Version == version }
}
