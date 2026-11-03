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
	"net/http"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// Link is the link's management API (linkdmanage.Client).
type Link interface {
	Search(ctx context.Context, tenantID string, query json.RawMessage, after string, limit int) (linkdmanage.SearchPage, error)
	CloseByFilter(ctx context.Context, command []byte, tenantID string, filter json.RawMessage, after string, limit int,
		dryRun bool) (linkdmanage.FilterPage, error)
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

// pageLimit is the filtered close's page: the link's own default, so one
// request's closes stay inside its ten-second budget at up to 100 ms an
// alert; whatever it does not reach answers 504 not_attempted and is found
// again. The dry run reads the link's largest page.
const pageLimit = 100

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
}

// Closer runs jobs and retries kept items. It is safe for concurrent use.
type Closer struct {
	link     Link
	commands *Commands
	pending  *Pending
	record   Record
	now      func() time.Time
	warn     func(error)

	mu sync.Mutex
	// progress is, per job filter, its dry run's cursor while it runs, then
	// how many matches it checked and how many items have been processed
	// since: closes stop at the checked count, and a new dry run checks
	// again. Held per term (Forget); a new leader checks again.
	progress         map[string]*progress
	unsupportedUntil time.Time
	counts           map[string]uint64
	// buckets is the edit close's per-alert walk, per bucket, within the
	// term.
	buckets map[string]string
}

// progress is one filter's state in the term: the dry run's cursor and
// count until it finishes, then the count it checked and the items
// processed since.
type progress struct {
	dryAfter           string
	dryCount           int
	checked, processed int
}

func New(options Options) (*Closer, error) {
	if options.Link == nil || options.Commands == nil || options.Pending == nil || options.Record == nil {
		return nil, errors.New("alarmd filterclose: the link, command store, pending store and episode record are required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Closer{link: options.Link, commands: options.Commands, pending: options.Pending, record: options.Record, now: now, warn: options.Warn,
		progress: map[string]*progress{}, counts: map[string]uint64{}, buckets: map[string]string{}}, nil
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
	closer.progress = map[string]*progress{}
	closer.buckets = map[string]string{}
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
	}
	closer.count(word, 1)
	return word
}

func filterKey(filter Filter) string {
	sum := sha256.Sum256(filter.body)
	return hex.EncodeToString(sum[:16])
}

// Run runs one job: the dry run against the expectation where the term has
// not checked this filter yet, then closes page by page, at most as many
// items as the dry run matched, until the filter runs out or the round
// does. It returns the job's word.
//
// Every page starts from the filter's head again on a new round: an alert
// closed no longer matches status=active, so the head is what is left. An
// item the link did not close for certain (busy, not attempted, version
// conflict) is found again that way and sent the same command, and the
// job answers OutcomeJobDoneRederived rather than OutcomeJobDone; an item
// whose close may have been saved is kept and retried by id
// (RetryPending).
func (closer *Closer) Run(ctx context.Context, job Job) string {
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
	progressKey := key + ":" + filterKey(job.Filter)
	closer.mu.Lock()
	state := closer.progress[progressKey]
	if state == nil || (state.checked > 0 && state.processed >= state.checked) {
		state = &progress{}
		closer.progress[progressKey] = state
	}
	closer.mu.Unlock()
	if state.checked == 0 {
		if job.expect != nil {
			expect, word := job.expect(ctx)
			if word != "" {
				closer.forgetProgress(progressKey)
				return word
			}
			job.Expect = expect
		}
		closer.count(job.Expect.word(), 1)
		allowed := job.Expect.allowed()
		matched, word := closer.dryRun(ctx, job, state, allowed+1)
		if word != "" {
			// The pages read so far are kept: the next round goes on from
			// the cursor rather than needing every page in one go.
			return word
		}
		switch {
		case matched > allowed:
			closer.forgetProgress(progressKey)
			closer.count(OutcomeExpectExceeded, 1)
			return OutcomeExpectExceeded
		case matched == 0:
			closer.forgetProgress(progressKey)
			closer.count(OutcomeNothingMatched, 1)
			return OutcomeNothingMatched
		}
		closer.mu.Lock()
		state.checked = matched
		closer.mu.Unlock()
	}
	after, rederived := "", 0
	for state.processed < state.checked {
		if ctx.Err() != nil {
			closer.count(OutcomeJobIncomplete, 1)
			return OutcomeJobIncomplete
		}
		limit := min(pageLimit, state.checked-state.processed)
		// The page's candidates first, by a dry run of the same request: a
		// real page whose answer is lost leaves its items unknown, and an
		// item whose close was saved but whose outputs failed no longer
		// matches active, so no filter finds it again (Linkd
		// ec55128e7 pkg/linkd/docs/reference/contracts/alert-close.md:
		// 133-135). Those ids are kept and retried by id.
		candidates, err := closer.link.CloseByFilter(ctx, nil, scope.TenantID, job.Filter.body, after, limit, true)
		if err != nil {
			return closer.failed(err)
		}
		page, err := closer.link.CloseByFilter(ctx, command.Bytes, scope.TenantID, job.Filter.body, after, limit, false)
		if err != nil {
			if status := linkdmanage.Status(err); status == 0 || status >= 500 {
				// Kept even when the round's own context ended the page:
				// the closes may have been saved all the same.
				closer.keepLost(context.WithoutCancel(ctx), job, command, candidates.Items)
			}
			return closer.failed(err)
		}
		rederived += closer.items(ctx, job, command, page.Items)
		closer.mu.Lock()
		state.processed += len(page.Items)
		closer.mu.Unlock()
		if page.Next == "" || len(page.Items) == 0 {
			closer.forgetProgress(progressKey)
			if rederived > 0 {
				// Left for the filter to find again: the decision is not
				// settled until a run of it closes everything it finds.
				closer.count(OutcomeJobDoneRederived, 1)
				return OutcomeJobDoneRederived
			}
			closer.count(OutcomeJobDone, 1)
			return OutcomeJobDone
		}
		after = page.Next
	}
	// The checked count is spent and the filter still matches: the next
	// round runs a new dry run before any further close.
	closer.forgetProgress(progressKey)
	closer.count(OutcomeJobIncomplete, 1)
	return OutcomeJobIncomplete
}

func (closer *Closer) forgetProgress(key string) {
	closer.mu.Lock()
	delete(closer.progress, key)
	closer.mu.Unlock()
}

// dryRun counts the filter's active matches, up to limit, carrying on
// from where the term's last dry run of the filter stopped.
func (closer *Closer) dryRun(ctx context.Context, job Job, state *progress, limit int) (int, string) {
	for {
		page, err := closer.link.CloseByFilter(ctx, nil, job.Filter.scope.TenantID, job.Filter.body, state.dryAfter, linkdmanage.MaxLimit, true)
		if err != nil {
			return 0, closer.failed(err)
		}
		closer.mu.Lock()
		state.dryCount += len(page.Items)
		state.dryAfter = page.Next
		matched := state.dryCount
		closer.mu.Unlock()
		if matched >= limit || page.Next == "" || len(page.Items) == 0 {
			return matched, ""
		}
	}
}

// keepLost keeps the candidates of a real page whose answer was lost, to
// be retried by id: each is counted as its keep's item outcome, the page
// once as OutcomePageLost.
func (closer *Closer) keepLost(ctx context.Context, job Job, command Command, candidates []linkdmanage.Item) {
	closer.count(OutcomePageLost, 1)
	for _, candidate := range candidates {
		closer.keep(ctx, job, command, candidate.AlertID)
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

// items counts a page's results, keeps the ones whose close may have been
// saved, and returns how many came back certainly not closed.
func (closer *Closer) items(ctx context.Context, job Job, command Command, items []linkdmanage.Item) int {
	rederived := 0
	for _, item := range items {
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
			default:
				closer.count(OutcomeItemConflictUnknown, 1)
			}
		case item.Status == http.StatusTooManyRequests,
			item.Status == http.StatusGatewayTimeout && item.ErrorCode == "not_attempted":
			closer.count(OutcomeItemRederived, 1)
			rederived++
		case item.Status >= 500:
			closer.keep(ctx, job, command, item.AlertID)
		default:
			closer.count(OutcomeItemRefused, 1)
		}
	}
	return rederived
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
