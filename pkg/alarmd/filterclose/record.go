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

// Result is a decision's end as its caller acts on it, from every job the
// decision ran - the alerts since the record's start checked against it,
// and the older ones alert by alert in batches, or only those where there
// is no record - so that no part's end is lost in another's:
//   - Word is the one to wait on, the parts' words combined by worse (the
//     highest class of NextAfter, else job_done when anything closed, else
//     nothing_matched): how long the caller waits before it sends the
//     decision again;
//   - Parts is each job's own word, in the order they ran (none where no
//     job ran, a word the decision answered before any): settled only when
//     every part is (Settled); a caller that takes a part's own end as
//     settled (the strategy scope close's settles) judges each part, so a
//     part settled by its own refusal does not settle another that has
//     more;
//   - Sent says a real close of the decision had items answered by the
//     link - closed, refused, busy, any item answer - which is a close
//     sent, not one the link closed: a decision the link refuses item by
//     item every round is sent every round;
//   - Closed is how many items the link answered closed across the jobs:
//     whether the decision closed anything, whatever its words.
type Result struct {
	Word   string
	Parts  []string
	Sent   bool
	Closed int
}

// only is the result of a decision that ended on one word before or as
// its one job.
func only(word string, tally jobTally) Result {
	return Result{Word: word, Parts: []string{word}, Sent: tally.answered(), Closed: tally.closed}
}

// add is one more job's end.
func (result *Result) add(word string, tally jobTally) {
	result.Parts = append(result.Parts, word)
	result.Sent = result.Sent || tally.answered()
	result.Closed += tally.closed
	result.Word = worse(result.Word, word)
}

// join is another part's jobs, in order.
func (result *Result) join(other Result) {
	result.Parts = append(result.Parts, other.Parts...)
	result.Sent = result.Sent || other.Sent
	result.Closed += other.Closed
	result.Word = worse(result.Word, other.Word)
}

// Settled says every part settled (job_done or nothing_matched).
func (result Result) Settled() bool {
	for _, word := range result.Parts {
		if !settledWord(word) {
			return false
		}
	}
	return true
}

// Again says some part got somewhere and has more (NextAgain).
func (result Result) Again() bool {
	for _, word := range result.Parts {
		if NextAfter(word) == NextAgain {
			return true
		}
	}
	return false
}

// recorded runs job over the alerts opened since the record's start
// against the record's entries match selects, decides the alerts opened
// before the start alert by alert with decide (the record cannot vouch for
// them: they opened before it was created), prunes the entries when the
// link holds none of the decision's alerts, and decides every alert one by
// one where the strategy has no record. Its Result holds every job's word
// and what they closed; its Word is the one to wait on.
func (closer *Closer) recorded(ctx context.Context, job Job, match func(openepisodes.Entry) bool,
	decide func(linkdmanage.Alert) bool) Result {
	if word := closer.refuseUnsupported(); word != "" {
		return only(word, jobTally{})
	}
	since, word := closer.readSince(ctx, job.Filter.scope, job.period)
	switch word {
	case OutcomeRecordMissing:
		return closer.perAlertResult(ctx, job, decide)
	case OutcomeRecordUnavailable:
		return only(word, jobTally{})
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
		return only(OutcomeFilterRefused, jobTally{})
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
	word, tally := closer.runTallied(ctx, recent)
	if word == OutcomeRecordMissing {
		return closer.perAlertResult(ctx, job, decide)
	}
	if word == OutcomeUnsupported {
		return only(word, tally)
	}
	result := Result{Word: OutcomeNothingMatched}
	result.add(word, tally)
	olderResult := closer.perAlertResult(ctx, older, decide)
	result.join(olderResult)
	// Settled both ways -- nothing left, or what was left closed -- the
	// link holds none of the decision's alerts, and the entries go. Kept
	// after a close, they outlived their episodes: the series' next episode
	// is a new alert at the link, and its entry was not written while the
	// old one stood, so the next strategy-wide close counted the strategy
	// short and was refused. Where a close did not
	// settle, the entries stay. A series that opens again between the record
	// read and this prune loses its entry here (its write found the old one
	// and wrote nothing); the next strategy-wide close then expects one short,
	// which refuses rather than closes, and the writer writes the entry back
	// within openepisodes.ReensureEvery.
	if result.Settled() {
		closer.prune(ctx, job.Filter.scope, prune)
	}
	return result
}

// worse is the word of two parts of one decision, the one its caller must
// act on (NextAfter): a held word before a transient one before one that
// got somewhere, so a part the same command will be answered the same way
// - or a link that failed it - is waited on even where the other part has
// more to close (a caller that runs again on progress would otherwise send
// the held part again at its own pace with it); else job_done when either
// closed something, else nothing_matched. Of two words of one class, the
// first.
func worse(a, b string) string {
	if rank(b) > rank(a) {
		return b
	}
	if rank(a) > 0 {
		return a
	}
	if a == OutcomeJobDone || b == OutcomeJobDone {
		return OutcomeJobDone
	}
	return OutcomeNothingMatched
}

// rank orders the classes for worse: settled 0, again 1, transient 2,
// held 3.
func rank(word string) int {
	switch NextAfter(word) {
	case NextAgain:
		return 1
	case NextTransient:
		return 2
	case NextHeld:
		return 3
	}
	return 0
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
// alert that left the decision meanwhile is not closed). Its Result holds
// each batch's word - and job_incomplete while the walk has pages left -
// and what they closed; its Word is the one to wait on, job_done when a
// close ran and settled, nothing_matched when nothing was decided.
func (closer *Closer) perAlert(ctx context.Context, job Job, decide func(linkdmanage.Alert) bool) string {
	return closer.perAlertResult(ctx, job, decide).Word
}

func (closer *Closer) perAlertResult(ctx context.Context, job Job, decide func(linkdmanage.Alert) bool) Result {
	started := time.Now()
	read, err := activeOnly(job.Filter)
	if err != nil {
		closer.count(OutcomeFilterRefused, 1)
		return only(OutcomeFilterRefused, jobTally{})
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
			return only(closer.failed(err), jobTally{})
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
		closer.notice(Notice{Outcome: OutcomeAlertsTruncated, Scope: job.Filter.scope, Decision: job.Decision, Read: n, Elapsed: time.Since(started)})
	}
	closer.mu.Lock()
	closer.buckets[walkKey] = after
	closer.mu.Unlock()
	closer.count(OutcomeAlertDecidedClose, len(ids))
	result := Result{Word: OutcomeNothingMatched}
	recheck := job.Recheck
	if recheck.body == nil {
		recheck = job.Filter
	}
	for start := 0; start < len(ids); start += linkdmanage.MaxLimit {
		batch := ids[start:min(len(ids), start+linkdmanage.MaxLimit)]
		byID, err := narrowed(job.Filter, terms(fieldAlertID, sorted(batch)))
		if err != nil {
			closer.count(OutcomeFilterRefused, 1)
			result.add(OutcomeFilterRefused, jobTally{})
			continue
		}
		batchWord, tally := closer.runTallied(ctx, Job{Filter: byID, Decision: job.Decision, Version: job.Version, Digest: job.Digest, Revision: job.Revision,
			Batch:  job.Batch + job.Filter.batch,
			Expect: Expectation{Count: len(batch), Source: ExpectDecidedIDs}, Reason: job.Reason, Recheck: recheck})
		if batchWord == OutcomeUnsupported {
			// Unsupported ends the decision: its one word, with what the
			// batches before it sent and closed.
			return Result{Word: batchWord, Parts: []string{batchWord}, Sent: result.Sent || tally.answered(),
				Closed: result.Closed + tally.closed}
		}
		result.add(batchWord, tally)
	}
	if after != "" {
		// Pages left: the walk is not done, whatever its batches answered.
		result.add(OutcomeJobIncomplete, jobTally{})
	}
	return result
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
