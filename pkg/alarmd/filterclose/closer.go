// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package filterclose closes the alerts a strategy decision ended through
// the alert link's own search and close by filter (Linkd
// b7e5beddf docs/reference/contracts/alert-search.md, alert-close.md),
// called by alarmd's control leader with the link's management token.
//
// A decision is about a set of alerts the link can select: a strategy's
// alerts (absent, inactive), one (version, facts digest, family) bucket of
// them (an edit), the older versions' alerts at a removed level, the
// alerts of removed targets. alarmd decides; the link selects and closes.
// Every filter is under its tenant, alarmd's event source and the strategy
// (Scope; the link adds only the tenant and status=active), and nothing is
// sent with a filter that does not carry all three.
//
// What has to hold before anything is closed:
//
//   - a dry run of the filter matches no more than an expectation from a
//     source independent of the search - alarmd's own record of the open
//     episodes it emitted for the strategy (package openepisodes), the
//     removed targets times the series per target, or the alert ids a
//     round decided one by one - plus a margin; otherwise the job is
//     refused and counted. Where the strategy has no record (it expired,
//     or the Redis lost it) nothing is closed by the filter: each candidate
//     alert is decided from its own labels and closed by its id;
//   - each decision has one command (operation id, reason, effective_at),
//     stored in the runtime Redis with the decision and sent byte for byte
//     on every request and retry, after a restart and on another leader;
//   - a retry is by the link's alert id. The link mints a new id for each
//     opening (b7e5beddf internal/lifecycle/id.go:35-36, plan.go:409-417),
//     and a close of an id that already ended answers already_closed or
//     invalid_transition, so no retry ends an alert that reopened since. A
//     filter that names series (fingerprints, targets) is bounded to the
//     alerts opened by the moment its decision was read (create_at), so it
//     does not select a reopening either;
//   - a failure of the search or of a whole request decides nothing: it is
//     counted and the next round decides again.
//
// The edit decision relies on the link keeping an alert's labels as its
// opening event wrote them (b7e5beddf internal/lifecycle/plan.go:417,
// Labels: event.Labels.Clone(); no lifecycle path assigns them again). That
// is the link's implementation, not a stated contract. The dependency is
// fail-safe: every close, the retries included, goes through the
// decision's filter, so an alert whose label moved is not selected - a
// changed label can only mean fewer closes.
package filterclose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// Link is the link's management API (linkdmanage.Client).
type Link interface {
	Search(ctx context.Context, tenantID string, query json.RawMessage, after string, limit int) (linkdmanage.SearchPage, error)
	CloseByFilter(ctx context.Context, command []byte, tenantID string, filter json.RawMessage, dryRun bool) (linkdmanage.FilterPage, error)
	CloseOne(ctx context.Context, alertID string, command []byte) (bool, error)
}

// Record is alarmd's own record of the episodes it opened
// (openepisodes.Store).
type Record interface {
	Load(ctx context.Context, key openepisodes.Key) (openepisodes.Record, error)
	Since(ctx context.Context, key openepisodes.Key) (time.Time, bool, error)
	Prune(ctx context.Context, key openepisodes.Key, fingerprints []string) error
}

// Expectation is how many alerts a job's filter may match, and where that
// number came from. The source is never the search the job runs.
type Expectation struct {
	Count  int
	Source string
}

// The expectation sources.
const (
	// ExpectDecidedIDs is the alerts a round decided one by one: the filter
	// names their ids, so it cannot match more.
	ExpectDecidedIDs = "decided_ids"
	// ExpectOwnRecord is alarmd's own record of the open episodes it
	// emitted for the strategy (openepisodes), the entries the decision
	// covers: its version for an inactive or edit close, all of them for an
	// absent close, the ones of targets outside the current scope for a
	// scope close.
	ExpectOwnRecord = "own_record"
)

// expectSlack is the least margin over an expectation.
const expectSlack = 20

// allowed is the most a dry run may match: the count plus a tenth, at
// least expectSlack. The margin covers what the count cannot: it is read
// at another moment than the dry run, and episodes open between; a worker
// writes the record after the broker's ACK, so an alert the link opened
// may not be in it yet (the write is queued); and the link may hold a
// series' alert opened by an event whose record write was lost. The record
// over-counts in the other direction - episodes the link ended on its own
// stay in it until pruned - which only widens what is allowed.
func (expect Expectation) allowed() int {
	return expect.Count + max(expect.Count/10, expectSlack)
}

func (expect Expectation) word() string {
	switch expect.Source {
	case ExpectDecidedIDs:
		return OutcomeExpectDecidedIDs
	case ExpectOwnRecord:
		return OutcomeExpectOwnRecord
	}
	return ""
}

// Job is one decision's filter, run in a round.
type Job struct {
	Filter Filter
	// Decision, Version, Digest and Revision are the decision: its word, the
	// version and facts digest it is about (empty where it is not version-
	// or bucket-scoped), and the strategy's current revision.
	Decision string
	Version  string
	Digest   string
	Revision int64
	Expect   Expectation
	// Batch is what the decision names beyond its filter's series (the
	// inactive period); the filter's own series or targets are added to
	// the decision's key by their builder.
	Batch string
	// Reason is the close reason the link records, at most 256 bytes.
	Reason string
	// Recheck is what a pending retry checks still names the alert; the
	// job's filter when zero. A target batch names its strategy instead: a
	// pending item does not carry the whole batch.
	Recheck Filter
	// expect, when set, is read in place of Expect before a dry run - only
	// then: a job whose term already checked its filter reads nothing. A
	// word it returns ends the job with that word.
	expect func(context.Context) (Expectation, string)
	// period is how often the strategy is evaluated, for how long its
	// record takes to vouch (settle); zero where it is not known.
	period time.Duration
}

// unsupportedRecheck is how long a link that answered 501, 404 or 405 is
// not asked again: every job meanwhile answers OutcomeUnsupported without
// a request, and an upgrade of the link is noticed within it.
const unsupportedRecheck = 10 * time.Minute

// Options configure a Closer.
type Options struct {
	Link     Link
	Commands *Commands
	Pending  *Pending
	// Record is alarmd's record of the episodes it opened, the expectation
	// of every strategy-wide close.
	Record Record
	Now    func() time.Time
	// Warn is told each time the link answers that it cannot serve the
	// search or the filtered close: filtered close is unsupported, and
	// nothing closes the decisions instead.
	Warn func(error)
	// Report is told each outcome a reader has to place by strategy to act
	// on -- a close refused past its expectation, a walk cut at its turn's
	// bound -- which the counts cannot say: they carry no strategy. Optional.
	Report func(Notice)
	// RequestBudget is how long one request to the link may take (the
	// client's timeout): UntilProgressStops starts another run only while a
	// request still fits. Zero: runs go on while the round lasts.
	RequestBudget time.Duration
	// ObserveRequest is told each filtered close request, dry run or real,
	// with how long it took and its result (one of RequestResults).
	// Optional.
	ObserveRequest func(Request)
	// ReportJob is told each job's end (Run): its strategy, decision and
	// word, what its real closes answered, and how long it ran - what the
	// counts cannot say, as they carry no strategy. Optional.
	ReportJob func(JobResult)
}

// JobResult is one job's end as ReportJob is told it.
type JobResult struct {
	Scope    Scope
	Decision string
	// Word is the job's word, what Run returned.
	Word string
	// Closed, Failed and Refused are what the job's real closes answered:
	// the items closed, the items answered other than 200 (the link's own
	// failed count), and among those the ones refused and left open (400,
	// 403, 409 source_unpublished, a 409 of a code alarmd does not know,
	// any other status).
	Closed, Failed, Refused int
	Elapsed                 time.Duration
}

// Request is one filtered close request as ObserveRequest is told it.
type Request struct {
	DryRun  bool
	Result  string
	Elapsed time.Duration
}

// The results of a filtered close request. A request the link did not
// answer: timeout (the request's own budget ran out), canceled (the round
// ended first), busy (429), unavailable (503), http_4xx, http_5xx (another
// status), transport (no status: the link was not reached or did not
// answer). invalid_response: a 200 whose body could not be read
// (linkdmanage.ErrInvalidResponse), or a request alarmd could not build
// and did not send (its command not a JSON object, its query invalid, its
// body past the link's bound). answer_too_large: a 200 whose body ran past
// the client's bound (linkdmanage.ErrAnswerTooLarge). A 200 with a top-level error
// code: query_failed (a later candidate query failed; the items read are
// answered, has_more set), error_unknown (a code alarmd does not know). A
// page the link answered: ok (no item failed, and every dry run), and for a
// real close whose items all failed,
// by their one status, items_busy (429 or 504 not_attempted),
// items_unavailable (503), items_5xx (another 5xx), items_failed (mixed or
// another); items_partial when some failed and some did not.
const (
	RequestOK               = "ok"
	RequestTimeout          = "timeout"
	RequestCanceled         = "canceled"
	RequestBusy             = "busy"
	RequestUnavailable      = "unavailable"
	RequestHTTP4xx          = "http_4xx"
	RequestHTTP5xx          = "http_5xx"
	RequestTransport        = "transport"
	RequestInvalidResponse  = "invalid_response"
	RequestAnswerTooLarge   = "answer_too_large"
	RequestQueryFailed      = "query_failed"
	RequestErrorUnknown     = "error_unknown"
	RequestItemsPartial     = "items_partial"
	RequestItemsBusy        = "items_busy"
	RequestItemsUnavailable = "items_unavailable"
	RequestItems5xx         = "items_5xx"
	RequestItemsFailed      = "items_failed"
)

// RequestResults are the results, every one, in a fixed order.
var RequestResults = []string{RequestOK, RequestTimeout, RequestCanceled, RequestBusy, RequestUnavailable, RequestHTTP4xx,
	RequestHTTP5xx, RequestTransport, RequestInvalidResponse, RequestAnswerTooLarge, RequestQueryFailed, RequestErrorUnknown,
	RequestItemsPartial, RequestItemsBusy, RequestItemsUnavailable, RequestItems5xx, RequestItemsFailed}

// Notice is one outcome Report is told, with what places it.
type Notice struct {
	Outcome  string
	Scope    Scope
	Decision string
	// Expected, Allowed and Matched are an expect_exceeded's: the
	// expectation's count and source, the most a dry run may match, and
	// what it matched.
	Expected int
	Source   string
	Allowed  int
	Matched  int
	// Read is an alerts_truncated's: the alerts the turn read before it
	// stopped.
	Read int
	// Elapsed is how long the job, or the turn's walk, had run when the
	// outcome was reached.
	Elapsed time.Duration
}

// Closer runs jobs and retries kept items. It is safe for concurrent use.
type Closer struct {
	link     Link
	commands *Commands
	pending  *Pending
	record   Record
	now      func() time.Time
	warn     func(error)
	report   func(Notice)
	observe  func(Request)
	jobs     func(JobResult)

	mu               sync.Mutex
	unsupportedUntil time.Time
	counts           map[string]uint64
	// buckets is the edit close's per-alert walk, per bucket, within the
	// term.
	buckets map[string]string
	// passes is, per bucket walked alert by alert, the walk in progress: the
	// Plans' key it decides against, whether every close it decided so far
	// settled, and the newest alert it read. walked is, per bucket, the
	// Plans' key a whole pass ended under with nothing left that it could
	// not close, and the newest alert it read: while the key holds every
	// alert read decides the same way it did, so the bucket is read again
	// only when an alert opened in it after that one (OutcomeWalkReopened;
	// otherwise OutcomeWalkRemembered). Both per term.
	passes map[string]*walkPass
	walked map[string]walkedBucket
	// requestBudget is how long one request may take, zero where it is not
	// known.
	requestBudget time.Duration
}

// walkPass is one bucket's alert-by-alert walk under one Plans' key.
type walkPass struct {
	key    string
	clean  bool
	newest time.Time
}

// walkedBucket is a bucket a whole pass ended for: the Plans' key and the
// newest alert the pass read, on the link's clock.
type walkedBucket struct {
	key    string
	newest time.Time
}

func New(options Options) (*Closer, error) {
	if options.Link == nil || options.Commands == nil || options.Pending == nil || options.Record == nil {
		return nil, errors.New("alarmd filterclose: the link, command store, pending store and episode record are required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Closer{link: options.Link, commands: options.Commands, pending: options.Pending, record: options.Record, now: now, warn: options.Warn, report: options.Report, observe: options.ObserveRequest,
		jobs: options.ReportJob, counts: map[string]uint64{}, buckets: map[string]string{},
		passes: map[string]*walkPass{}, walked: map[string]walkedBucket{}, requestBudget: options.RequestBudget}, nil
}

// Unsupported says the link answered within unsupportedRecheck that it
// cannot serve the search or the filtered close.
func (closer *Closer) Unsupported() bool {
	if closer == nil {
		return true
	}
	closer.mu.Lock()
	defer closer.mu.Unlock()
	return closer.now().Before(closer.unsupportedUntil)
}

// refuseUnsupported counts and answers OutcomeUnsupported while the link
// is known not to serve the routes; empty otherwise.
func (closer *Closer) refuseUnsupported() string {
	if !closer.Unsupported() {
		return ""
	}
	closer.count(OutcomeUnsupported, 1)
	return OutcomeUnsupported
}

// Forget drops the term's progress: a new term checks every job again.
func (closer *Closer) Forget() {
	closer.mu.Lock()
	closer.buckets = map[string]string{}
	closer.passes = map[string]*walkPass{}
	closer.walked = map[string]walkedBucket{}
	closer.mu.Unlock()
}

// Stats is every outcome's count.
func (closer *Closer) Stats() map[string]uint64 {
	counts := make(map[string]uint64, len(Outcomes))
	if closer == nil {
		for _, outcome := range Outcomes {
			counts[outcome] = 0
		}
		return counts
	}
	closer.mu.Lock()
	defer closer.mu.Unlock()
	for _, outcome := range Outcomes {
		counts[outcome] = closer.counts[outcome]
	}
	return counts
}

func (closer *Closer) count(outcome string, n int) {
	if n <= 0 || outcome == "" {
		return
	}
	closer.mu.Lock()
	closer.counts[outcome] += uint64(n)
	closer.mu.Unlock()
}

// failed is the word for a request that did not answer 200, counted.
func (closer *Closer) failed(err error) string {
	word := OutcomeLinkFailed
	switch status := linkdmanage.Status(err); {
	case linkdmanage.Unsupported(err):
		closer.mu.Lock()
		closer.unsupportedUntil = closer.now().Add(unsupportedRecheck)
		closer.mu.Unlock()
		if closer.warn != nil {
			closer.warn(err)
		}
		word = OutcomeUnsupported
	case status == http.StatusTooManyRequests:
		word = OutcomeLinkBusy
	case status >= 400 && status < 500:
		word = OutcomeLinkRefused
	case errors.Is(err, linkdmanage.ErrBodyTooLarge), errors.Is(err, linkdmanage.ErrInvalidRequest):
		word = OutcomeLinkRefused
	case errors.Is(err, linkdmanage.ErrAnswerTooLarge):
		word = OutcomeLinkAnswerTooLarge
	}
	closer.count(word, 1)
	return word
}

func filterKey(filter Filter) string {
	sum := sha256.Sum256(filter.body)
	return hex.EncodeToString(sum[:16])
}

// Run runs one job: requests, each a dry run checked against the
// expectation and then the real close, while the link answers has_more,
// until it has no more, the closes reach the expectation, or the round
// ends. It returns the job's word.
//
// Every request starts from the filter's head again: an alert closed no
// longer matches status=active, so the head is what is left. An
// item the link did not close for certain (busy, not attempted, version
// conflict) is found again that way and sent the same command, and the
// job answers OutcomeJobDoneRederived rather than OutcomeJobDone; an item
// whose close may have been saved is kept and retried by id
// (RetryPending). An item the link refused and left open is not named
// again in the run: the head is read again with it, and the run goes on
// with the ids it has not sent, so refused alerts whose ids sort first do
// not hold back the ones after them; the job then ends job_done_refused.
//
// Each job's end is told to ReportJob, whichever word ends it.
func (closer *Closer) Run(ctx context.Context, job Job) string {
	word, _ := closer.runTallied(ctx, job)
	return word
}

// runTallied is Run, with what the job's real closes answered.
func (closer *Closer) runTallied(ctx context.Context, job Job) (string, jobTally) {
	started := time.Now()
	var tally jobTally
	word := closer.run(ctx, job, started, &tally)
	if closer.jobs != nil {
		closer.jobs(JobResult{Scope: job.Filter.scope, Decision: job.Decision, Word: word, Closed: tally.closed, Failed: tally.failed,
			Refused: tally.refused, Elapsed: time.Since(started)})
	}
	return word, tally
}

// jobTally is what one job's real closes answered, for its JobResult.
type jobTally struct {
	closed, failed, refused int
	// items is how many items the job's real closes were answered, any
	// status: the link's answer item by item, whatever its own failed
	// count says.
	items int
}

// answered says a real close of the job had items answered by the link:
// closed, refused, busy, not attempted, uncertain. A real close the link
// refused or failed whole, or never sent, answered none.
func (tally jobTally) answered() bool { return tally.items > 0 }

func (closer *Closer) run(ctx context.Context, job Job, started time.Time, tally *jobTally) string {
	if word := closer.refuseUnsupported(); word != "" {
		return word
	}
	if requireScope(job.Filter) != nil {
		closer.count(OutcomeFilterRefused, 1)
		return OutcomeFilterRefused
	}
	scope := job.Filter.scope
	key := decisionKey(scope, job.Version, job.Digest, job.Decision, job.Batch+job.Filter.batch, job.Revision)
	command, err := closer.commands.Resolve(ctx, key, scope.TenantID, job.Reason, closer.now())
	if err != nil {
		closer.count(OutcomeCommandUnavailable, 1)
		return OutcomeCommandUnavailable
	}
	if job.expect != nil {
		expect, word := job.expect(ctx)
		if word != "" {
			return word
		}
		job.Expect = expect
	}
	closer.count(job.Expect.word(), 1)
	allowed := job.Expect.allowed()
	// The link pages nothing: a request closes, or on a dry run enumerates,
	// what its own budget allows and says has_more (alert-close.md, Linkd
	// c5e2443a3). The job sends requests while there is more, the round
	// lasts and the closes stay within the expectation.
	closed, rederived, refused := 0, 0, 0
	// refusedIDs is every id a real close of this run had the link refuse
	// and leave open (items). Such an alert still matches, so every later
	// dry run reads it again, first where its id sorts first; the next
	// close names none of them (withIDs), so ids refused ahead of the rest
	// do not keep the rest from being sent within the same run.
	refusedIDs := map[string]bool{}
	for {
		if word := closer.roundSpent(ctx); word != "" {
			return word
		}
		// The request's candidates first, by a dry run of the same filter,
		// before every real close and not once a job. It is the
		// expectation's check, and it names what the real close may close:
		// the close's filter is narrowed to the ids it read (withIDs), so an
		// alert that matches only after the dry run is not closed by this
		// request, and when the close's answer is lost the ids sent are every
		// alert it may have closed - an item whose close was saved but whose
		// outputs failed no longer matches active, so no filter and no later
		// dry run finds it again, and only those ids complete it. A dry run
		// reads ids only - no lease, no write, no close batch
		// (alert-close.md) - so it costs the request far less than its close;
		// only a match set large enough to fill the link's budget with ids
		// spends the budget on it.
		candidates, err := closer.closeByFilter(ctx, nil, scope.TenantID, job.Filter.body, true)
		if err != nil {
			return closer.failed(err)
		}
		closer.answered(candidates)
		// The dry run's count is exact when the link enumerated every match
		// (no has_more); otherwise there is at least one more, which is
		// counted so a dry run cut by the link's budget at exactly what is
		// allowed is refused rather than taken as all. The checks are against
		// what the expectation still allows after this job's closes so far;
		// a request closes only ids its dry run read, at most that many, so
		// the job's closes never pass what is allowed.
		matched, remaining := len(candidates.Items), allowed-closed
		if candidates.HasMore {
			matched++
		}
		switch {
		case matched > remaining && closed == 0:
			closer.count(OutcomeExpectExceeded, 1)
			closer.notice(Notice{Outcome: OutcomeExpectExceeded, Scope: job.Filter.scope, Decision: job.Decision,
				Expected: job.Expect.Count, Source: job.Expect.Source, Allowed: allowed, Matched: matched, Elapsed: time.Since(started)})
			return OutcomeExpectExceeded
		case matched > remaining:
			// This job closed up to its expectation and the filter still
			// matches: no further request this run; the next round checks
			// again from the record as it then stands.
			closer.count(OutcomeCloseCapReached, 1)
			closer.count(OutcomeJobIncomplete, 1)
			return OutcomeJobIncomplete
		case len(candidates.Items) == 0 && !candidates.HasMore:
			if closed > 0 || rederived > 0 || refused > 0 {
				return closer.settled(rederived, refused)
			}
			closer.count(OutcomeNothingMatched, 1)
			return OutcomeNothingMatched
		}
		// What this run has not sent yet: the ids the dry run read less the
		// ones a close of this run had refused. None left is the end of the
		// run - settled where the dry run read every match, so the job ends
		// refused rather than incomplete; not settled where the link has more
		// past the refused ids, which the next round reads again.
		unsent := len(candidates.Items)
		for _, item := range candidates.Items {
			if refusedIDs[item.AlertID] {
				unsent--
			}
		}
		if len(candidates.Items) > 0 && unsent == 0 {
			if !candidates.HasMore {
				return closer.settled(rederived, refused)
			}
			closer.count(OutcomeJobIncomplete, 1)
			return OutcomeJobIncomplete
		}
		// Not started without a request's budget left: a close the round cut
		// off would lose its answer and keep every candidate to retry by id.
		if word := closer.roundSpent(ctx); word != "" {
			return word
		}
		narrowed, ids, err := job.Filter.withIDs(candidates.Items, remaining, refusedIDs)
		if err != nil {
			closer.count(OutcomeFilterRefused, 1)
			return OutcomeFilterRefused
		}
		refusedBefore := len(refusedIDs)
		page, err := closer.closeByFilter(ctx, command.Bytes, scope.TenantID, narrowed.body, false)
		if err != nil {
			if status := linkdmanage.Status(err); status == 0 || status >= 500 {
				// Kept even when the round's own context ended the request:
				// the closes may have been saved all the same.
				closer.keepLost(context.WithoutCancel(ctx), job, command, ids)
			}
			return closer.failed(err)
		}
		closer.answered(page)
		again, kept := closer.items(ctx, job, command, page.Items, refusedIDs)
		rederived, refused = rederived+again, refused+kept
		closed += page.Closed
		tally.closed, tally.failed, tally.refused = closed, tally.failed+page.Failed, refused
		tally.items += len(page.Items)
		// More is left when the close ran out of budget on its ids, when
		// the dry run read only some of the matches, or when not every id
		// it had not sent fit this request.
		if !page.HasMore && !candidates.HasMore && len(ids) == unsent {
			return closer.settled(rederived, refused)
		}
		if page.Closed == 0 && len(refusedIDs) == refusedBefore {
			// More is left and this request neither closed anything nor had
			// an id refused it had not had refused before (the link's budget
			// spent before a batch, items busy): another request now gets no
			// further. Not settled; the next run or round goes on. A request
			// whose ids were all refused did get further: the next one names
			// other ids.
			closer.count(OutcomeJobIncomplete, 1)
			return OutcomeJobIncomplete
		}
	}
}

// settled is the word of a job whose last request had no more to match:
// done, or not settled where items came back certainly not closed - found
// again by the filter when the decision runs again (job_done_rederived), or
// refused by the link and still open (job_done_refused). Neither prunes the
// record: its entries stand for alerts still open.
func (closer *Closer) settled(rederived, refused int) string {
	switch {
	case rederived > 0:
		closer.count(OutcomeJobDoneRederived, 1)
		return OutcomeJobDoneRederived
	case refused > 0:
		closer.count(OutcomeJobDoneRefused, 1)
		return OutcomeJobDoneRefused
	}
	closer.count(OutcomeJobDone, 1)
	return OutcomeJobDone
}

// roundSpent ends a job before a request the round could not wait for:
// the round's context is done, or what is left of it is less than one
// request's budget (RequestBudget), as UntilProgressStops does before a run.
func (closer *Closer) roundSpent(ctx context.Context) string {
	if ctx.Err() != nil {
		closer.count(OutcomeJobIncomplete, 1)
		return OutcomeJobIncomplete
	}
	if deadline, ok := ctx.Deadline(); ok && closer.requestBudget > 0 && time.Until(deadline) < closer.requestBudget {
		closer.count(OutcomeRoundSpent, 1)
		closer.count(OutcomeJobIncomplete, 1)
		return OutcomeJobIncomplete
	}
	return ""
}

// answered counts the top-level error code of a 200: query_failed, where a
// later candidate query failed after some were read (the items read are
// answered, and has_more is set), or a code alarmd does not know.
func (closer *Closer) answered(page linkdmanage.FilterPage) {
	switch page.ErrorCode {
	case "":
	case linkdmanage.QueryFailed:
		closer.count(OutcomeLinkQueryFailed, 1)
	default:
		closer.count(OutcomeLinkErrorUnknown, 1)
	}
}

// keepLost keeps the ids a real close whose answer was lost was narrowed
// to, to be retried by id: each is counted as its keep's item outcome, the
// request once as OutcomePageLost.
func (closer *Closer) keepLost(ctx context.Context, job Job, command Command, ids []string) {
	closer.count(OutcomePageLost, 1)
	for _, id := range ids {
		closer.keep(ctx, job, command, id)
	}
}

// keep keeps one item whose close may have been saved, counted.
func (closer *Closer) keep(ctx context.Context, job Job, command Command, alertID string) {
	recheck := job.Recheck
	if recheck.body == nil {
		recheck = job.Filter
	}
	scope := job.Filter.scope
	err := closer.pending.Keep(ctx, pendingItem{TenantID: scope.TenantID, EventSourceID: scope.EventSourceID, StrategyID: scope.StrategyID,
		AlertID: alertID, OperationID: command.ID, Command: command.Bytes, Recheck: recheck.body, FirstAt: closer.now().Unix()})
	switch {
	case err == nil:
		closer.count(OutcomeItemPending, 1)
	case errors.Is(err, errPendingFull):
		closer.count(OutcomeItemPendingFull, 1)
	default:
		closer.count(OutcomeItemPendingLost, 1)
	}
}

// items counts a real close's item results, keeps the ones whose close may
// have been saved, and returns how many came back certainly not closed and
// found again by the filter (rederived), and how many the link refused and
// left open (refused: 400, 403, 409 source_unpublished, a 409 of a code
// alarmd does not know, any other status): none of these says the alert
// ended, so its record entry stays. The refused ids are added to
// refusedIDs when it is not nil.
func (closer *Closer) items(ctx context.Context, job Job, command Command, items []linkdmanage.Item, refusedIDs map[string]bool) (int, int) {
	rederived, refused := 0, 0
	for _, item := range items {
		before := refused
		switch {
		case item.Status == http.StatusOK && item.AlreadyClosed:
			closer.count(OutcomeItemAlreadyClosed, 1)
		case item.Status == http.StatusOK:
			closer.count(OutcomeItemClosed, 1)
		case item.Status == http.StatusConflict:
			switch item.ErrorCode {
			case linkdmanage.ConflictInvalidTransition:
				closer.count(OutcomeItemEndedOtherwise, 1)
			case linkdmanage.ConflictVersion:
				closer.count(OutcomeItemRederived, 1)
				rederived++
			case linkdmanage.ConflictSourceUnpublished:
				closer.count(OutcomeItemSourceUnpublished, 1)
				refused++
			default:
				closer.count(OutcomeItemConflictUnknown, 1)
				refused++
			}
		case item.Status == http.StatusTooManyRequests,
			item.Status == http.StatusGatewayTimeout && item.ErrorCode == "not_attempted":
			closer.count(OutcomeItemRederived, 1)
			rederived++
		case item.Status == http.StatusServiceUnavailable:
			// The link refused before the close ran (its close resources not
			// configured, alert-close.md "503"): not saved, so not kept to
			// retry by id. Certainly not closed as 429 is, so the job is not
			// settled and its record is not pruned; a page that closed
			// nothing is not run again this round (UntilProgressStops), and the
			// next round's filter finds the alert again.
			closer.count(OutcomeItemUnavailable, 1)
			rederived++
		case item.Status >= 500:
			closer.keep(ctx, job, command, item.AlertID)
		case item.Status == http.StatusNotFound:
			// The alert was enumerated and is gone before its close: not
			// closed by this command and not active, so nothing to retry.
			closer.count(OutcomeItemNotFound, 1)
		case item.Status == http.StatusBadRequest:
			// The link refused the command for this item (invalid_command):
			// the same command would be refused again.
			closer.count(OutcomeItemInvalidCommand, 1)
			refused++
		case item.Status == http.StatusForbidden:
			// The alert is not the command's tenant's (tenant_mismatch).
			closer.count(OutcomeItemTenantMismatch, 1)
			refused++
		default:
			closer.count(OutcomeItemRefused, 1)
			refused++
		}
		if refused > before && refusedIDs != nil {
			refusedIDs[item.AlertID] = true
		}
	}
	return rederived, refused
}

// RetryPending retries up to n kept items by id, each with its own
// command, once the decision's filter still names its alert (any status:
// an alert whose close was saved is closed, and the retry completes its
// outputs). A whole-request failure of the link ends the pass.
func (closer *Closer) RetryPending(ctx context.Context, n int) {
	if closer.refuseUnsupported() != "" {
		return
	}
	items, unreadable, err := closer.pending.Next(ctx, n)
	closer.count(OutcomePendingUnreadable, unreadable)
	if err != nil {
		closer.count(OutcomePendingUnavailable, 1)
		return
	}
	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		if item.expired(closer.now()) {
			closer.drop(ctx, item, OutcomePendingGivenUp)
			continue
		}
		recheck, err := withAlertID(Filter{scope: Scope{TenantID: item.TenantID, EventSourceID: item.EventSourceID, StrategyID: item.StrategyID},
			body: item.Recheck}, item.AlertID)
		if err != nil {
			closer.drop(ctx, item, OutcomePendingUnreadable)
			continue
		}
		page, err := closer.link.Search(ctx, item.TenantID, recheck.body, "", 1)
		if err != nil {
			closer.failed(err)
			return
		}
		if len(page.Items) == 0 || page.Items[0].AlertID != item.AlertID {
			closer.drop(ctx, item, OutcomePendingLeftDecision)
			continue
		}
		already, err := closer.link.CloseOne(ctx, item.AlertID, item.Command)
		switch status := linkdmanage.Status(err); {
		case err == nil && already:
			closer.drop(ctx, item, OutcomePendingAlreadyClosed)
		case err == nil:
			closer.drop(ctx, item, OutcomePendingClosed)
		case status == http.StatusConflict:
			switch linkdmanage.ConflictKind(err) {
			case linkdmanage.ConflictInvalidTransition:
				closer.drop(ctx, item, OutcomePendingEndedOther)
			case linkdmanage.ConflictSourceUnpublished:
				closer.drop(ctx, item, OutcomeItemSourceUnpublished)
			case linkdmanage.ConflictVersion:
				closer.again(ctx, item)
			default:
				// A 409 whose text names no kind this build knows: counted,
				// and retried within the item's bounds.
				closer.count(OutcomeItemConflictUnknown, 1)
				closer.again(ctx, item)
			}
		case status == http.StatusNotFound:
			closer.drop(ctx, item, OutcomePendingNotFound)
		case status == http.StatusTooManyRequests:
			closer.again(ctx, item)
			return
		case status == 0 || status >= 500:
			closer.again(ctx, item)
		default:
			closer.drop(ctx, item, OutcomePendingRefused)
		}
	}
}

func (closer *Closer) drop(ctx context.Context, item pendingItem, outcome string) {
	if err := closer.pending.Done(ctx, item); err != nil {
		closer.count(OutcomePendingUnavailable, 1)
		return
	}
	closer.count(outcome, 1)
}

func (closer *Closer) again(ctx context.Context, item pendingItem) {
	if err := closer.pending.Again(ctx, item); err != nil {
		closer.count(OutcomePendingUnavailable, 1)
		return
	}
	closer.count(OutcomePendingRetryFailed, 1)
}

// PendingSize is how many items are kept, for the metric; -1 when the
// store cannot say.
func (closer *Closer) PendingSize(ctx context.Context) int64 {
	if closer == nil {
		return 0
	}
	size, err := closer.pending.Size(ctx)
	if err != nil {
		return -1
	}
	return size
}

// closeByFilter is the link's filtered close, told to ObserveRequest.
func (closer *Closer) closeByFilter(ctx context.Context, command []byte, tenantID string, filter json.RawMessage, dryRun bool) (linkdmanage.FilterPage, error) {
	sent := time.Now()
	page, err := closer.link.CloseByFilter(ctx, command, tenantID, filter, dryRun)
	if closer.observe != nil {
		closer.observe(Request{DryRun: dryRun, Result: requestResult(ctx, page, err, dryRun), Elapsed: time.Since(sent)})
	}
	return page, err
}

// requestResult is a filtered close request's result (RequestResults).
func requestResult(ctx context.Context, page linkdmanage.FilterPage, err error, dryRun bool) string {
	if err != nil {
		status := linkdmanage.Status(err)
		switch {
		case ctx.Err() != nil:
			return RequestCanceled
		case timedOut(ctx, err):
			return RequestTimeout
		case errors.Is(err, linkdmanage.ErrAnswerTooLarge):
			return RequestAnswerTooLarge
		case errors.Is(err, linkdmanage.ErrInvalidResponse), errors.Is(err, linkdmanage.ErrCommandNotObject),
			errors.Is(err, linkdmanage.ErrInvalidRequest), errors.Is(err, linkdmanage.ErrBodyTooLarge):
			return RequestInvalidResponse
		case status == http.StatusTooManyRequests:
			return RequestBusy
		case status == http.StatusServiceUnavailable:
			return RequestUnavailable
		case status >= 500:
			return RequestHTTP5xx
		case status >= 400:
			return RequestHTTP4xx
		}
		return RequestTransport
	}
	switch page.ErrorCode {
	case "":
	case linkdmanage.QueryFailed:
		return RequestQueryFailed
	default:
		return RequestErrorUnknown
	}
	if dryRun {
		return RequestOK
	}
	failed, word := 0, ""
	for _, item := range page.Items {
		if item.Status == http.StatusOK {
			continue
		}
		failed++
		class := RequestItemsFailed
		switch {
		case item.Status == http.StatusTooManyRequests, item.Status == http.StatusGatewayTimeout && item.ErrorCode == "not_attempted":
			class = RequestItemsBusy
		case item.Status == http.StatusServiceUnavailable:
			class = RequestItemsUnavailable
		case item.Status >= 500:
			class = RequestItems5xx
		}
		if word == "" {
			word = class
		} else if word != class {
			word = RequestItemsFailed
		}
	}
	switch {
	case failed == 0:
		return RequestOK
	case failed < len(page.Items):
		return RequestItemsPartial
	}
	return word
}

// notice tells Report, when there is one.
func (closer *Closer) notice(notice Notice) {
	if closer.report != nil {
		closer.report(notice)
	}
}

// timedOut is a request the client gave up on while the round still had
// time: the link may still have been closing the page's alerts.
func timedOut(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var network net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &network) && network.Timeout())
}

// UntilProgressStops runs a decision again within the round while it got
// somewhere: some part has more (Result.Again) and the run closed
// something (Result.Closed), with a request's budget left. It returns the
// last run's words, with what every run sent and closed. It reads each
// part, so a part with more is run again beside a part that waits (worse
// hands the waiting one on as the Word), and a run that closed nothing is
// not run again whatever it answered: progress is the run's own Closed,
// not what the process closed meanwhile. A failed request (link_failed)
// is not run again within the round.
func (closer *Closer) UntilProgressStops(ctx context.Context, run func() Result) Result {
	closed, sent := 0, false
	for {
		result := run()
		closed, sent = closed+result.Closed, sent || result.Sent
		if !result.Again() || result.Closed == 0 || ctx.Err() != nil {
			result.Closed, result.Sent = closed, sent
			return result
		}
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < closer.requestBudget {
			result.Closed, result.Sent = closed, sent
			return result
		}
		closer.count(OutcomeJobAgain, 1)
	}
}
