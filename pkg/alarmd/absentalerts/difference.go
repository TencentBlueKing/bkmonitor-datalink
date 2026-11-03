// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package absentalerts answers one question the rest of the program cannot:
// which strategies still hold unrecovered alerts after the strategy itself
// stopped existing.
//
// Every other close path starts from a Plan. A replica owns a Query Group,
// the Query Group holds the strategy's Plan, and the Plan says whether the
// strategy is inactive right now. A strategy that was disabled or deleted
// has no Plan and no owner, so no replica is looking at it, and its alerts
// stay open with nothing left to recover them. The difference is therefore
// taken where the whole strategy snapshot is in hand, which is the control
// leader, and the close is pushed from there.
//
// The difference is the alert link's roster minus the strategy snapshot:
// every strategy the link says holds an unrecovered alert, less every
// strategy the source says exists. The roster is the link's own list, read
// through its Console, walked by the link on its own connection. This exists
// only where the link does: a deployment without it has no roster, and
// nothing here runs.
//
// The strategy cache is the answer to "does this strategy exist": a strategy
// the cache no longer lists is one alarmd does not run, and its unrecovered
// alerts have nothing left to recover them. The cache is not read twice or
// reinterpreted here; the snapshot is the control plane's own read of it. What
// is added is only what one read cannot say: that the read was a good one
// (read at all, not old, and not empty unless the writer states it), and that
// the strategy stayed missing - for Bounds.MinReads deciding rounds, under
// two observations. How much smaller a list is than the one before is not
// judged: a strategy leaves the list when it is gone, and a writer that drops
// strategies on error is the writer's to fix.
//
// A strategy the writer marked as departed (Round.Marks: disabled or deleted,
// with the revision it left at, <prefix>.departed_strategies) takes
// the fast path instead: it is closed on the round after the one that first
// read it absent under that same mark, without the reads the implicit path
// waits for. One round between is what lets a strategy disabled and enabled
// again within it come back before anything closes: enabling it removes the
// mark and raises its revision. The mark's revision is the strategy record's
// (record_revision, which enabling and disabling move), not the
// executed content's that a Plan carries (strategy_revision): the two are
// not compared. What is compared is the mark against itself - the same state
// and revision on both rounds, and again just before the close is sent. The
// two paths are counted apart (Decision.Path).
//
// A strategy the cache lists and alarmd's publication refused stands with
// the absent ones when the refusal is for a capability this build lacks and
// no configuration change brings back (fleet.ClosesAsUnsupported): alarmd
// will not run it, so its unrecovered alerts have nothing to recover them
// either, and its close says it is no longer detected rather than gone
// (Round.Refused). A refusal a configuration change or a later read can
// undo is not a candidate: the strategy comes back on its own.
//
// The roster can only err by missing a strategy or listing one twice, which
// costs a close and never makes one; and a close the link receives for an
// alert that is no longer active is a no-op on its side. So the link's own
// account of its maintenance is not a gate: a roster from a link whose
// maintenance has stopped is an old one, which can only miss a strategy, and
// absence is decided by the snapshot alone. Refusing on it would turn "may
// close fewer" into "close none" - and a link whose maintenance fails under
// the alerts of a strategy that is gone is exactly the one whose close would
// remove them.
package absentalerts

import (
	"sort"
	"time"
)

// Key identifies a strategy in both sets being compared: the alert link's
// roster and the source snapshot.
type Key struct {
	TenantID   string
	StrategyID string
}

// Identity is what a close needs about a strategy beyond its key: the
// business it belonged to and the snapshot revision. The catalog remembers
// it for the strategies it let go while this process was watching; for the
// rest it is read from the alert's own record at close time, which is where
// the labels the alert was created with are still written down.
type Identity struct {
	BusinessID int64
	Revision   int64
	// GlobalBusiness is a strategy whose alerts are each filed under the
	// business they are about: BusinessID is the strategy's own, and each
	// close carries its alert's business instead.
	GlobalBusiness bool
}

// Absence is this loop's own memory of a candidate: when it first found the
// strategy missing from the snapshot, which observation found it, and how
// many deciding rounds have found it missing since. The observation is kept
// because the source reconciler reuses one observation across rounds, and a
// candidate confirmed twice against one observation was confirmed once.
//
// Rounds are counted rather than time measured, so a release that restarts
// the leader costs no more than the rounds it skips: the memory is kept in
// the runtime Redis across terms (Memory), and TermReads, which is not kept,
// is what holds a new term back from closing on its first round.
type Absence struct {
	Since       time.Time
	Observation string
	// Reads is how many deciding rounds found the strategy missing, the one
	// that first did included.
	Reads int
	// Confirmed is whether a round of this term found it missing under an
	// observation of the source other than the first this term read it
	// under (TermObservation). Not carried into a new term (Restore): a
	// strategy that came back and left again while no leader watched would
	// otherwise close on a confirmation made before it came back.
	Confirmed bool
	// TermReads is how many of those rounds ran in this leader's term, and
	// TermObservation the observation the first of them read.
	TermReads       int
	TermObservation string
	// Mark is the writer's departure mark the last of them read with it,
	// zero for none, and MarkReads how many consecutive rounds read that
	// same mark (state and revision).
	Mark      Mark
	MarkReads int
}

// Mark is the writer's statement that a strategy left: disabled or deleted,
// at the strategy record's revision. The writer keeps one per strategy that
// leaves the list for either reason and removes it when the strategy comes
// back (strategy-direct-cache-publication.md, departed_strategies).
type Mark struct {
	TenantID string
	State    string
	Revision int64
}

// The states a mark names.
const (
	MarkDisabled = "disabled"
	MarkDeleted  = "deleted"
)

// The paths a candidate is decided on.
const (
	// PathImplicit: missing from the snapshot with no mark, Bounds.MinReads
	// rounds under two observations.
	PathImplicit = "implicit"
	// PathDeparted: missing under the writer's mark, the round after the one
	// that first read it so.
	PathDeparted = "departed"
)

// Round is one difference and everything it may decide on.
type Round struct {
	// LinkRead says the link's roster was read at all this round. Without it
	// there is no difference to take.
	LinkRead bool
	// Roster is every strategy the link listed with at least one member in
	// its open alert set, with how many members the link read in it.
	Roster map[Key]int
	// RosterUnreadable is how many listed strategies the link could not read
	// the set of. Not in Roster, because not read is not empty.
	RosterUnreadable int
	// RosterComplete says the walk reached its end. An incomplete walk is a
	// smaller roster, which costs closes and never makes one.
	RosterComplete bool
	// Refused is, by strategy id, every strategy the source lists that alarmd
	// does not run because its publication refused it for a capability this
	// build lacks, with that refusal's reason. Its unrecovered alerts have
	// nothing left to judge them either, so it is a candidate as an absent
	// strategy is, under the same grace and the same two observations, and
	// its close says it is no longer detected rather than gone.
	Refused map[string]string
	// SnapshotStrategies is every strategy the source says exists right now.
	// SnapshotUsable says it was observed rather than assumed, and
	// SnapshotObservation identifies which observation it is.
	SnapshotStrategies  map[Key]struct{}
	SnapshotUsable      bool
	SnapshotObservation string
	SnapshotAgeSeconds  int64
	// WriterHoldsLastGood is the writer's statement, made about this very
	// list, that a strategy leaves it only for a fact about the strategy
	// itself and never because publishing it failed. Only under it is a
	// strategy missing from the list gone, so only under it does a round
	// decide: a list without it is refused (RefusalWriterUnstated; an empty
	// one RefusalSnapshotEmpty), and an empty list under it is a fact, every
	// strategy gone.
	WriterHoldsLastGood bool
	// Identities is what the catalog remembers about the strategies it let
	// go. A candidate not here is still closed; its identity is read from its
	// alerts.
	Identities map[Key]Identity
	// FirstAbsent is this loop's memory of when each candidate was first
	// found missing by this loop itself.
	FirstAbsent map[Key]Absence
	// Marks is the writer's departure marks by strategy id, read this round;
	// nil where the writer keeps none or they could not be read, which only
	// leaves every candidate to the implicit path.
	Marks map[string]Mark
	// After is the last strategy the previous round decided to close; this
	// round's walk over the candidates starts just past it.
	After Key
	Now   time.Time
}

// Decision is what a round decided about one candidate: one of the four
// per-candidate outcomes OutcomeWithinGrace, OutcomeUnconfirmed,
// OutcomeDeferred and OutcomeCloseDecided. AbsentSince is zero when the memory did
// not hold the candidate, which keeps it within the grace on every round.
type Decision struct {
	Key         Key
	Outcome     string
	AbsentSince time.Time
	// Refused is the capability the strategy was refused for, empty for a
	// strategy absent from the snapshot.
	Refused string
	// Path is PathDeparted when the strategy is decided on the writer's
	// mark, PathImplicit otherwise.
	Path string
}

// Absent is one strategy the round decided to close.
type Absent struct {
	Key Key
	// Identity is zero when the catalog does not remember the strategy; the
	// close then reads it from the alert's own record.
	Identity Identity
	// AbsentSince is when this loop first found the strategy missing.
	AbsentSince time.Time
	// Refused is the capability the strategy was refused for (Round.Refused):
	// its close says alarmd no longer detects it. Empty for a strategy absent
	// from the snapshot.
	Refused string
	// Path is the path it was decided on, and Mark, on PathDeparted, the
	// mark it was decided under: the close's decision is disabled or deleted
	// after it, and is sent only while a read of that strategy's mark just
	// before it still holds it.
	Path string
	Mark Mark
}

// The refusals of a whole round. Each names the fact that was not good
// enough to decide on, because "closed nothing" has to be answerable with
// why.
const (
	// RefusalNone is a round that decided. It is a word rather than an empty
	// string so that the metric has a cell for it: how many rounds decided
	// is the denominator every refusal count is read against.
	RefusalNone = "none"
	// RefusalLinkUnavailable: the link's roster could not be read. Nothing
	// read is not an empty roster.
	RefusalLinkUnavailable = "link_unavailable"
	// RefusalSnapshotUnusable: the snapshot was not observed this round. An
	// unread snapshot is not an empty one, and reading it as empty would
	// close every unrecovered alert in the deployment.
	RefusalSnapshotUnusable = "snapshot_unusable"
	// RefusalSnapshotEmpty: the snapshot was observed, lists no strategy at
	// all, and the writer made no statement about it. On it every
	// unrecovered alert in the deployment would be a candidate at once, and
	// nothing says the list is the writer's word rather than one that lost
	// its content. Under the writer's statement (Round.WriterHoldsLastGood)
	// an empty list is a fact and the round decides.
	RefusalSnapshotEmpty = "snapshot_empty"
	// RefusalSnapshotStale: the observation is older than this round may
	// decide on. Strategies created since it was read would read as absent.
	RefusalSnapshotStale = "snapshot_stale"
	// RefusalWriterUnstated: the list was observed and is not empty, and its
	// writer made no statement about it (Round.WriterHoldsLastGood): no
	// publication_semantics saying it holds the last good document, or one
	// about another set of strategy ids. A writer without the statement
	// drops strategies that still exist and adds them back later, so a
	// strategy missing from its list is not known to be gone; the round
	// closes nothing, and the platform's own close stays the closer there.
	// Checked after every other refusal: an empty list without the
	// statement is snapshot_empty, a stale one snapshot_stale.
	RefusalWriterUnstated = "writer_unstated"
)

// Refusals is the closed list of whole-round refusals.
var Refusals = []string{RefusalNone, RefusalLinkUnavailable, RefusalSnapshotUnusable, RefusalSnapshotEmpty,
	RefusalSnapshotStale, RefusalWriterUnstated}

// The per-strategy outcomes of the decision. Every candidate the round did
// not close lands on one of these, and every one of them is counted.
const (
	// OutcomeCloseDecided: absent from the snapshot for the grace under two
	// observations, and within this round's bound. Decided, not closed: what
	// went out is OutcomeCloseSent, and whether the link acted on it is read
	// per alert on the link.
	OutcomeCloseDecided = "close_decided"
	// OutcomeWithinGrace: absent, but not yet for Bounds.MinReads deciding
	// rounds, or not yet for two rounds of this leader's term. A leader that
	// has just been elected cannot close on its first round.
	OutcomeWithinGrace = "within_grace"
	// OutcomeUnconfirmed: absent for enough rounds, but every round that
	// found it absent read the same observation of the source.
	OutcomeUnconfirmed = "unconfirmed"
	// OutcomeDeferred: decided, but this round's close bound was already
	// spent. Counted per strategy; the next round takes it.
	OutcomeDeferred = "deferred"
	// OutcomeIndexUnreadable: the link listed the strategy and could not read
	// its set. Counted per strategy and per round, and never folded into "it
	// has no alerts".
	OutcomeIndexUnreadable = "index_unreadable"
	// OutcomeDepartedCloseDecided: missing under the writer's mark on two
	// consecutive rounds, the same state and revision, both of this term,
	// and within this round's bound. The fast path's close_decided.
	OutcomeDepartedCloseDecided = "departed_close_decided"
	// OutcomeDepartedWaiting: missing under the writer's mark, on the first
	// round that read that mark (or the first of this term). The next round
	// decides it unless the strategy came back.
	OutcomeDepartedWaiting = "departed_waiting"
)

// The outcomes of carrying a decision out. They are counted in different
// units, which the metric's help has to say.
const (
	// OutcomeIdentityUnknown: no business could be found for the strategy -
	// the catalog does not remember it and none of its alerts' records names
	// one. Named rather than guessed: a close sent to the wrong business is
	// worse than an alert that stays open. Per strategy.
	OutcomeIdentityUnknown = "identity_unknown"
	// OutcomeRevisionUnknown: the business is known and the snapshot
	// revision is not. The close contract requires one. Per strategy.
	OutcomeRevisionUnknown = "revision_unknown"
	// OutcomeBusinessUnknown: the strategy files each alert under the
	// business it is about, and this alert's record could not be read or
	// names none. That alert is not closed this round. Per alert.
	OutcomeBusinessUnknown = "business_unknown"
	// OutcomeEvidenceUnavailable: the strategy's alerts could not be read
	// from the link, so there is nothing to address a close to. Per
	// strategy.
	OutcomeEvidenceUnavailable = "evidence_unavailable"
	// OutcomeEvidenceTruncated: the strategy's alerts were read, and the
	// link's answer was incomplete or past the reader's caps. No reader of
	// this build reports it (the Console-era listing is gone); the word
	// stays in the closed list until the outcomes are cut. The alerts it listed are closed
	// as usual and the rest are left for a later round, when the strategy is
	// decided again; when none listed is this deployment's, that is not "it
	// has none of ours". Per strategy, beside its other outcomes.
	OutcomeEvidenceTruncated = "evidence_truncated"
	// OutcomeCloseSent: a strategy's close decision that sent at least one
	// real close whose items the link answered (closed, refused or busy,
	// item by item; filterclose.Result.Sent). One per decision, as
	// OutcomeCloseDecided. Sent, not closed: the link closes the alert or
	// does not, and a strategy it does not close is sent again every round.
	OutcomeCloseSent = "close_sent"
	// OutcomeSendFailed: the absent close's job line word for a close not
	// sent (observeCloseJob). Not counted: the filtered close's counts are
	// linkd_filter_close_total's.
	OutcomeSendFailed = "send_failed"
	// OutcomeNotLeader: this replica is not the control leader and takes no
	// difference. Counted so that a deployment where no replica ever ran a
	// round is not read as a deployment with nothing to close.
	OutcomeNotLeader = "not_leader"
	// OutcomeMemoryFull: a candidate the bounded memory would not hold. Such
	// a strategy is never closed, so the bound being reached is a reading.
	OutcomeMemoryFull = "memory_full"
	// OutcomeProducerForeign: an alert another producer wrote. The link's
	// set is keyed by strategy, not by who created the alert, and a set
	// shared by several sources holds all of theirs. Per alert, never
	// closed.
	OutcomeProducerForeign = "producer_foreign"
	// OutcomeProducerUnknown: an alert that names no producer. Counted and
	// never closed: "it does not say it is someone else's" is not "it is
	// ours".
	OutcomeProducerUnknown = "producer_unknown"
	// OutcomeDepartedReappeared: decided on the fast path, and the latest
	// snapshot, read again just before this strategy's close, lists it: it
	// came back. Not closed. Per strategy.
	OutcomeDepartedReappeared = "departed_reappeared"
	// OutcomeDepartedMarkChanged: decided on the fast path, and its mark,
	// read again (one HGET) just before its close, is no longer the one it
	// was decided under (gone, another state or another revision). Not
	// closed; the next round decides again. Per strategy.
	OutcomeDepartedMarkChanged = "departed_mark_changed"
	// OutcomeMarksUnreadable: the writer's marks could not be read this
	// round, or a strategy's mark could not be read again just before its
	// close. Every candidate is left to the implicit path; a fast-path
	// decision whose re-check could not read is not closed. Per round, once
	// whichever read failed.
	OutcomeMarksUnreadable = "marks_unreadable"
	// OutcomeMarkInvalid: one mark that did not decode under the contract,
	// or that names another tenant than the alerts' (Counts.MarkForeign):
	// not believed. Per mark and per round.
	OutcomeMarkInvalid = "mark_invalid"
	// OutcomeMemoryLoadFailed: a new term could not read the memory the last
	// term kept, and starts its candidates from nothing. Per term.
	OutcomeMemoryLoadFailed = "memory_load_failed"
	// OutcomeMemoryInvalid: one kept candidate that did not decode, skipped:
	// that candidate starts over. Per field.
	OutcomeMemoryInvalid = "memory_invalid"
	// OutcomeMemorySaveFailed: a round's memory could not be written; this
	// term goes on from its own, and a next term starts from the last write
	// that landed. Per round.
	OutcomeMemorySaveFailed = "memory_save_failed"
	// OutcomeMemoryFenced: the write was refused because this replica no
	// longer holds the control lease. The round closes nothing. Per round.
	OutcomeMemoryFenced = "memory_fenced"
)

// Outcomes is the closed list, for the metric that reports every cell.
var Outcomes = []string{OutcomeCloseDecided, OutcomeWithinGrace, OutcomeUnconfirmed,
	OutcomeDeferred, OutcomeIndexUnreadable, OutcomeDepartedCloseDecided, OutcomeDepartedWaiting,
	OutcomeIdentityUnknown, OutcomeRevisionUnknown, OutcomeBusinessUnknown,
	OutcomeEvidenceUnavailable, OutcomeEvidenceTruncated, OutcomeCloseSent, OutcomeSendFailed,
	OutcomeNotLeader, OutcomeMemoryFull, OutcomeProducerForeign, OutcomeProducerUnknown,
	OutcomeDepartedReappeared, OutcomeDepartedMarkChanged, OutcomeMarksUnreadable, OutcomeMarkInvalid,
	OutcomeMemoryLoadFailed, OutcomeMemoryInvalid, OutcomeMemorySaveFailed, OutcomeMemoryFenced}

// Counts is what the round measured. The denominators are reported with
// every reading, because a zero means different things - nothing was
// absent, or nothing was decided - and without them a reader cannot tell a
// healthy deployment from a difference that never ran.
type Counts struct {
	Roster             int
	RosterUnreadable   int
	SnapshotStrategies int
	// Candidates is the difference this round acts on: listed by the link,
	// not listed by the snapshot.
	Candidates   int
	CloseDecided int
	WithinGrace  int
	Unconfirmed  int
	Deferred     int
	// The fast path's, counted apart from the implicit path's above.
	DepartedCloseDecided int
	DepartedWaiting      int
	// MarkForeign is the candidates whose mark names another tenant: not
	// believed, so the implicit path decides them, and counted as an
	// invalid mark.
	MarkForeign int
}

// Bounds are the gates a round is decided under.
type Bounds struct {
	// MinReads is how many deciding rounds must have found a strategy with
	// no mark missing before its alerts are closed. Rounds, not time: a
	// round that refused or did not run is not one, so the bound is how
	// many times the source was seen without it, whatever restarts fell in
	// between.
	MinReads int
	// MaxSnapshotAge is how old the observation may be. A snapshot older
	// than this has not seen the strategies created since, and every one of
	// them holding an alert would read as absent.
	MaxSnapshotAge time.Duration
	// MaxCloseStrategies bounds how many strategies one round closes, so a
	// backlog is worked off over rounds instead of in one batch.
	MaxCloseStrategies int
}

// Result is the round's decision. Close is what the round decided to
// close, which is not the same as what it sent: the send is the caller's,
// and its own guards - whose alerts they are, which business and revision
// they carry - can still hold a decided strategy back, so the two are
// counted apart.
type Result struct {
	Close []Absent
	// Decisions is every candidate's decision, in the candidates' order
	// (lessKey) rather than the walk's, so that a reader can page them by
	// key. Each outcome's count in Counts is how many of these carry it.
	Decisions []Decision
	Counts    Counts
	Refusal   string
}

// Candidates is the first half of the difference: the strategies with
// unrecovered alerts that the snapshot does not list, or the refusal that
// stopped the round before it named any. It is separate from the decision
// so that the caller can start a candidate's grace clock before the
// decision reads it, without the decision itself mutating memory.
func Candidates(round Round, bounds Bounds) ([]Key, Counts, string) {
	counts := Counts{Roster: len(round.Roster), RosterUnreadable: round.RosterUnreadable,
		SnapshotStrategies: len(round.SnapshotStrategies)}
	if !round.LinkRead {
		return nil, counts, RefusalLinkUnavailable
	}
	if !round.SnapshotUsable || round.SnapshotObservation == "" {
		return nil, counts, RefusalSnapshotUnusable
	}
	if len(round.SnapshotStrategies) == 0 && !round.WriterHoldsLastGood {
		return nil, counts, RefusalSnapshotEmpty
	}
	if bounds.MaxSnapshotAge > 0 && time.Duration(round.SnapshotAgeSeconds)*time.Second > bounds.MaxSnapshotAge {
		return nil, counts, RefusalSnapshotStale
	}
	if !round.WriterHoldsLastGood {
		return nil, counts, RefusalWriterUnstated
	}
	// Presence is decided by the strategy id alone. The snapshot lists a
	// strategy whose source document lost its tenant with an empty one, while
	// the link keys the same strategy under the tenant its alerts carry; by
	// the full key that strategy would read as absent although it exists.
	// Strategy ids are unique across tenants, and matching on less can only
	// make a strategy look present, which costs a close and never makes one.
	existing := strategyIDs(round.SnapshotStrategies)
	candidates := make([]Key, 0)
	for key := range round.Roster {
		_, present := existing[key.StrategyID]
		if _, refused := round.Refused[key.StrategyID]; present && !refused {
			continue
		}
		candidates = append(candidates, key)
	}
	counts.Candidates = len(candidates)
	sortKeys(candidates)
	return candidates, counts, RefusalNone
}

func strategyIDs(keys map[Key]struct{}) map[string]struct{} {
	ids := make(map[string]struct{}, len(keys))
	for key := range keys {
		ids[key.StrategyID] = struct{}{}
	}
	return ids
}

// Compute takes the whole difference. It reads nothing and writes nothing:
// the caller supplies both sides and the memory, and applies what comes
// back.
func Compute(round Round, bounds Bounds) Result {
	candidates, counts, refusal := Candidates(round, bounds)
	if refusal != RefusalNone {
		return Result{Counts: counts, Refusal: refusal}
	}
	result := Result{Counts: counts, Refusal: RefusalNone, Decisions: make([]Decision, len(candidates))}
	var listed map[string]struct{}
	if len(round.Refused) > 0 {
		listed = strategyIDs(round.SnapshotStrategies)
	}
	start := rotation(candidates, round.After)
	for step := range candidates {
		at := (start + step) % len(candidates)
		key := candidates[at]
		absence, tracked := round.FirstAbsent[key]
		decision := Decision{Key: key, Path: PathImplicit}
		if _, present := listed[key.StrategyID]; present {
			// A refused strategy the snapshot no longer lists is gone, and
			// closes as gone.
			decision.Refused = round.Refused[key.StrategyID]
		}
		if tracked {
			decision.AbsentSince = absence.Since
		}
		mark, marked := markFor(round, key)
		if foreignMark(round, key) {
			result.Counts.MarkForeign++
		}
		if marked && decision.Refused == "" {
			decision.Path = PathDeparted
		}
		full := bounds.MaxCloseStrategies > 0 && len(result.Close) >= bounds.MaxCloseStrategies
		switch {
		case decision.Path == PathDeparted && (!tracked || absence.Mark != mark || absence.MarkReads < 2 || absence.TermReads < 2):
			decision.Outcome = OutcomeDepartedWaiting
			result.Counts.DepartedWaiting++
		case decision.Path == PathImplicit && (!tracked || absence.Reads < bounds.MinReads || absence.TermReads < 2):
			decision.Outcome = OutcomeWithinGrace
			result.Counts.WithinGrace++
		case decision.Path == PathImplicit && !absence.Confirmed:
			decision.Outcome = OutcomeUnconfirmed
			result.Counts.Unconfirmed++
		case full:
			decision.Outcome = OutcomeDeferred
			result.Counts.Deferred++
		case decision.Path == PathDeparted:
			decision.Outcome = OutcomeDepartedCloseDecided
			result.Close = append(result.Close, Absent{Key: key, Identity: round.Identities[key], AbsentSince: absence.Since,
				Path: PathDeparted, Mark: mark})
			result.Counts.DepartedCloseDecided++
		default:
			decision.Outcome = OutcomeCloseDecided
			result.Close = append(result.Close, Absent{Key: key, Identity: round.Identities[key], AbsentSince: absence.Since,
				Refused: decision.Refused, Path: PathImplicit})
			result.Counts.CloseDecided++
		}
		result.Decisions[at] = decision
	}
	return result
}

// markFor is the mark the round read for the candidate: by strategy id,
// and only one of a known state whose tenant is the candidate's or names
// none.
func markFor(round Round, key Key) (Mark, bool) {
	mark, ok := round.Marks[key.StrategyID]
	if !ok || (mark.State != MarkDisabled && mark.State != MarkDeleted) || (mark.TenantID != "" && mark.TenantID != key.TenantID) {
		return Mark{}, false
	}
	return mark, true
}

// foreignMark is a mark the round read for the candidate's strategy id that
// names another tenant.
func foreignMark(round Round, key Key) bool {
	mark, ok := round.Marks[key.StrategyID]
	return ok && mark.TenantID != "" && mark.TenantID != key.TenantID
}

// rotation is where the walk over the candidates starts: just after the
// last strategy a previous round closed, so that a strategy which never
// yields a close - every alert another producer's, no identity to be found -
// cannot hold the front of the order and spend the bound on every round.
func rotation(candidates []Key, after Key) int {
	if after == (Key{}) || len(candidates) == 0 {
		return 0
	}
	return sort.Search(len(candidates), func(i int) bool { return lessKey(after, candidates[i]) }) % len(candidates)
}

// LessKey is the order candidates are listed, remembered and walked in:
// tenant, then strategy id, both compared as strings ("100" before "20").
func LessKey(a, b Key) bool { return lessKey(a, b) }

func lessKey(a, b Key) bool {
	if a.TenantID != b.TenantID {
		return a.TenantID < b.TenantID
	}
	return a.StrategyID < b.StrategyID
}

func sortKeys(keys []Key) {
	sort.Slice(keys, func(i, j int) bool { return lessKey(keys[i], keys[j]) })
}
