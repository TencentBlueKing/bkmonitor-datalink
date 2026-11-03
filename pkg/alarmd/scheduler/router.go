// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

// Package scheduler selects Query Group ownership and starts frozen Slot
// execution. It does not contain Access, evaluation or completion logic.
package scheduler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

var ErrNoEligibleWorker = errors.New("alarmd scheduler: no eligible worker")

// ErrDeclinedEverywhere is ErrNoEligibleWorker when the workers that were
// eligible all decline the Query Group: an execution of it hung on each of
// them in turn, which is the Query Group's own bug and not any worker's
// (design 02 section 6.5). It is ErrNoEligibleWorker to every errors.Is.
var ErrDeclinedEverywhere = fmt.Errorf("%w: every eligible worker declines it", ErrNoEligibleWorker)

type WorkerEligibility interface {
	Eligible(execution.QueryGroupIdentity, ownership.WorkerRegistration, time.Time) bool
}

type staticWorkerEligibility struct {
	required ownership.WorkerCompatibility
}

func NewStaticWorkerEligibility(required ownership.WorkerCompatibility) (WorkerEligibility, error) {
	if err := required.Validate(); err != nil {
		return nil, err
	}
	return staticWorkerEligibility{required: required}, nil
}

func (eligibility staticWorkerEligibility) Eligible(
	_ execution.QueryGroupIdentity,
	worker ownership.WorkerRegistration,
	_ time.Time,
) bool {
	return worker.Compatibility().Matches(eligibility.required)
}

type Router struct {
	additionalEligibility WorkerEligibility
}

func NewRouter(additionalEligibility WorkerEligibility) *Router {
	return &Router{additionalEligibility: additionalEligibility}
}

func (router *Router) Select(
	queryGroup execution.QueryGroupIdentity,
	workers []ownership.WorkerRegistration,
	at time.Time,
) (ownership.WorkerRegistration, error) {
	if router == nil || queryGroup == "" || at.IsZero() {
		return ownership.WorkerRegistration{}, errors.New("alarmd scheduler: invalid routing request")
	}
	var selected ownership.WorkerRegistration
	var selectedScore [sha256.Size]byte
	found, declined := false, false
	seen := make(map[string]struct{}, len(workers))
	for _, worker := range workers {
		if err := worker.Validate(); err != nil {
			return ownership.WorkerRegistration{}, err
		}
		if _, duplicate := seen[worker.WorkerID]; duplicate {
			return ownership.WorkerRegistration{}, errors.New("alarmd scheduler: duplicate worker identity")
		}
		seen[worker.WorkerID] = struct{}{}
		if worker.AssignmentReadiness != ownership.WorkerReady || !worker.ExpiresAt.After(at) {
			continue
		}
		if router.additionalEligibility != nil && !router.additionalEligibility.Eligible(queryGroup, worker, at) {
			continue
		}
		if worker.Declines(queryGroup) {
			// It has an execution of this Query Group hung and let it go
			// (design 02 section 6.5): never placed back while it says so.
			declined = true
			continue
		}
		score := sha256.Sum256([]byte(string(queryGroup) + "\x00" + worker.WorkerID))
		if !found || bytes.Compare(score[:], selectedScore[:]) > 0 {
			selected, selectedScore, found = worker, score, true
		}
	}
	if !found {
		if declined {
			return ownership.WorkerRegistration{}, ErrDeclinedEverywhere
		}
		return ownership.WorkerRegistration{}, ErrNoEligibleWorker
	}
	return selected, nil
}

// incumbentEligible reports whether the Worker named by workerID is in the
// ready set under the same membership rules Select applies: a valid READY
// registration that has not expired at the given time and, when configured,
// the additional eligibility. Select itself is left untouched.
func (router *Router) incumbentEligible(
	queryGroup execution.QueryGroupIdentity,
	workerID string,
	workers []ownership.WorkerRegistration,
	at time.Time,
) bool {
	return router.incumbentEligibleIn(queryGroup, workerID, indexReadyWorkers(workers), at)
}

// readyWorkerIndex is one round's ready set, kept both ways.
//
// The set is read once per round and then consulted once per Query Group to
// ask whether that Query Group's incumbent is still in it. Walking the slice
// each time makes the round Q*N in a comparison a lookup answers, and the
// walk is not where any decision is taken -- the same worker is found either
// way.
//
// The slice is kept beside the map, and Select still takes the slice. Select
// is not a lookup: it validates every worker and refuses a duplicate worker
// identity, and both of those are statements about the list it was given.
// Handing it a list rebuilt from the map would drop the duplicate refusal
// entirely and make "which invalid worker is reported" depend on map order.
type readyWorkerIndex struct {
	ordered []ownership.WorkerRegistration
	byID    map[string]ownership.WorkerRegistration
}

func indexReadyWorkers(workers []ownership.WorkerRegistration) readyWorkerIndex {
	index := readyWorkerIndex{ordered: workers, byID: make(map[string]ownership.WorkerRegistration, len(workers))}
	for _, worker := range workers {
		// First wins, which is what scanning the slice in order did.
		if _, seen := index.byID[worker.WorkerID]; !seen {
			index.byID[worker.WorkerID] = worker
		}
	}
	return index
}

// alive says the worker is in the ready set with a valid, READY registration
// that has not expired and does not decline the Query Group: everything
// incumbentEligibleIn asks except the additional eligibility. A holder like
// that is still running the Query Group, so moving it is a handover; one
// that declines it has let it go and is not.
func (index readyWorkerIndex) alive(queryGroup execution.QueryGroupIdentity, workerID string, at time.Time) bool {
	worker, found := index.byID[workerID]
	return found && worker.Validate() == nil && worker.AssignmentReadiness == ownership.WorkerReady && worker.ExpiresAt.After(at) &&
		!worker.Declines(queryGroup)
}

// declinedBy is which workers of the ready set decline queryGroup, and the
// stage each one's execution hung in.
func (index readyWorkerIndex) declinedBy(queryGroup execution.QueryGroupIdentity) DeclinedEverywhere {
	facts := DeclinedEverywhere{QueryGroup: string(queryGroup)}
	for _, worker := range index.ordered {
		for _, declined := range worker.Declined {
			if declined.QueryGroup == string(queryGroup) {
				facts.Hops++
				facts.Stages = append(facts.Stages, DeclinerStage{Worker: worker.WorkerID, Stage: declined.Stage})
			}
		}
	}
	return facts
}

func (router *Router) incumbentEligibleIn(
	queryGroup execution.QueryGroupIdentity,
	workerID string,
	workers readyWorkerIndex,
	at time.Time,
) bool {
	if router == nil || workerID == "" {
		return false
	}
	worker, found := workers.byID[workerID]
	if !found {
		return false
	}
	if worker.Validate() != nil || worker.AssignmentReadiness != ownership.WorkerReady || !worker.ExpiresAt.After(at) ||
		worker.Declines(queryGroup) {
		return false
	}
	return router.additionalEligibility == nil || router.additionalEligibility.Eligible(queryGroup, worker, at)
}

type AssignmentStore interface {
	ListReadyWorkers(context.Context, time.Time) ([]ownership.WorkerRegistration, ownership.ControlReadStats, error)
	ReadAssignment(context.Context, execution.QueryGroupIdentity) (ownership.AssignmentRecord, error)
	// ReadAssignments is how a round reads the records of many Query Groups.
	// It is in this interface rather than behind a type assertion because a
	// method the round depends on and only some implementations have is a
	// method that silently does not run: the fakes would all carry it, the
	// production store would be the one that quietly did not, and the round
	// would go on spending one round trip per Query Group with every test
	// green.
	ReadAssignments(
		context.Context,
		[]execution.QueryGroupIdentity,
	) (map[execution.QueryGroupIdentity]ownership.AssignmentRecord, ownership.ControlReadStats, error)
	PublishAssignment(
		context.Context,
		ownership.PublicationAuthority,
		ownership.AssignmentDecision,
	) (ownership.AssignmentRecord, error)
}

type Reconciler struct {
	router    *Router
	store     AssignmentStore
	timelines TimelineRevisionSource
}

// TimelineRevisionSource answers which revision a Query Group's Schedule
// timeline record is at, for a placement to write onto the record it
// creates (decision-016 batch 4). Zero, and no error, is "no timeline yet":
// the record then says nothing until a cutover stamps it.
type TimelineRevisionSource interface {
	TimelineRecordRevision(context.Context, execution.QueryGroupIdentity) (uint64, error)
}

// WithTimelineRevisions gives the reconciler where to read a timeline's
// revision from. It is asked only for a Query Group whose record does not
// exist or does not say - a placement, a record from before the field, or a
// Query Group with no timeline yet, which is asked again each round until
// it has one. The reads go through the control cache, and the Query Groups
// in that state are few.
func (reconciler *Reconciler) WithTimelineRevisions(source TimelineRevisionSource) *Reconciler {
	if reconciler != nil {
		reconciler.timelines = source
	}
	return reconciler
}

// timelineRevisionFor is the revision a decision for queryGroup names: the
// record's own when it says, otherwise what the source says, otherwise zero.
// A source that fails leaves zero; the record then keeps saying nothing and
// the next round asks again, which is the self-correcting side to be on - a
// wrong number here would be believed by every renewal.
func (reconciler *Reconciler) timelineRevisionFor(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	current ownership.AssignmentRecord,
	hasCurrent bool,
) uint64 {
	if hasCurrent && current.TimelineRecordRevision != 0 {
		return 0
	}
	if reconciler.timelines == nil {
		return 0
	}
	revision, err := reconciler.timelines.TimelineRecordRevision(ctx, queryGroup)
	if err != nil {
		return 0
	}
	return revision
}

// ContentScopePolicy is what a reconcile round does about the content
// scope on each Assignment record (decision-016). The zero value leaves
// scopes exactly as they are, so a caller that knows nothing about them
// changes nothing.
type ContentScopePolicy int

const (
	// ContentScopesUntouched: records keep whatever scope they carry.
	ContentScopesUntouched ContentScopePolicy = iota
	// ContentScopesDeclared: every ready worker takes part in the content
	// contract, so each record is brought to the scope the current
	// publication names for its Query Group (a pending change under a live
	// lease, direct otherwise, by the store's rule).
	ContentScopesDeclared
	// ContentScopesWithdrawn: a ready worker does not take part, so every
	// record that names or pends a scope has it withdrawn. Withdrawing
	// refuses nobody and is written directly.
	ContentScopesWithdrawn
)

// ContentScopes is a round's content policy and, when declaring, the scope
// each Query Group should be on: the ObjectDigest the current publication
// names for it. A Query Group absent from Digests is left untouched even
// when declaring -- the round does not know its content, and an unknown
// content is not a withdrawal.
type ContentScopes struct {
	Policy  ContentScopePolicy
	Digests map[execution.QueryGroupIdentity]string
}

// wanted is the scope decision for one record that keeps its desired
// worker: the scope to publish, whether to withdraw, and whether either is
// needed at all.
func (scopes ContentScopes) wanted(queryGroup execution.QueryGroupIdentity, current ownership.AssignmentRecord) (scope string, withdraw, needed bool) {
	switch scopes.Policy {
	case ContentScopesDeclared:
		digest, known := scopes.Digests[queryGroup]
		if !known || digest == "" {
			return "", false, false
		}
		if current.PendingContentScope == digest {
			return "", false, false
		}
		if current.ContentScope == digest && current.PendingContentScope == "" {
			return "", false, false
		}
		// Either the record names other content, or it names this content
		// with a change pending towards yet another -- a scope written ahead
		// of a cutover that then did not happen. Publishing the current
		// content again is how the store cancels that change.
		return digest, false, true
	case ContentScopesWithdrawn:
		if current.ContentScope == "" && current.PendingContentScope == "" {
			return "", false, false
		}
		return "", true, true
	default:
		return "", false, false
	}
}

func NewReconciler(router *Router, store AssignmentStore) (*Reconciler, error) {
	if router == nil || store == nil {
		return nil, errors.New("alarmd scheduler: router and Assignment store are required")
	}
	return &Reconciler{router: router, store: store}, nil
}

// ListReadyWorkers reads the ready set once for a reconcile round. A round
// reconciles every Query Group against this one set: listing per Query
// Group costs a registry read per Query Group and, worse, lets one round
// decide different Query Groups on different ready sets, a world state that
// never existed at any instant.
func (reconciler *Reconciler) ListReadyWorkers(
	ctx context.Context,
	at time.Time,
) ([]ownership.WorkerRegistration, ownership.ControlReadStats, error) {
	if reconciler == nil {
		return nil, ownership.ControlReadStats{}, errors.New("alarmd scheduler: initialized reconciler is required")
	}
	return reconciler.store.ListReadyWorkers(ctx, at)
}

// Reconcile settles one Query Group on its own, listing the ready set for
// it. A round over many Query Groups must list once and use ReconcileWith.
func (reconciler *Reconciler) Reconcile(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	queryGroup execution.QueryGroupIdentity,
	at time.Time,
) (ownership.AssignmentRecord, error) {
	workers, _, err := reconciler.ListReadyWorkers(ctx, at)
	if err != nil {
		return ownership.AssignmentRecord{}, err
	}
	return reconciler.ReconcileWith(ctx, authority, queryGroup, workers, at)
}

// ReconcileWith settles one Query Group against a ready set the caller read
// for the round: a ready, eligible incumbent keeps its Assignment; otherwise
// rendezvous placement over that set publishes a new one under the given
// authority. It never lists workers itself.
func (reconciler *Reconciler) ReconcileWith(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	queryGroup execution.QueryGroupIdentity,
	workers []ownership.WorkerRegistration,
	at time.Time,
) (ownership.AssignmentRecord, error) {
	if reconciler == nil {
		return ownership.AssignmentRecord{}, errors.New("alarmd scheduler: initialized reconciler is required")
	}
	current, err := reconciler.store.ReadAssignment(ctx, queryGroup)
	hasCurrent := err == nil
	if !hasCurrent && !errors.Is(err, ownership.ErrAssignmentAbsent) {
		return ownership.AssignmentRecord{}, err
	}
	record, outcome, err := reconciler.settle(ctx, authority, queryGroup, current, hasCurrent, indexReadyWorkers(workers), at, ContentScopes{}, nil)
	if err == nil && outcome == settleUnplaceable {
		return ownership.AssignmentRecord{}, ErrNoEligibleWorker
	}
	return record, err
}

// ReconcileRound settles every Query Group of a round against one ready set,
// reading all of their Assignment records first.
//
// The records are read in one bounded batch rather than one at a time. That
// is the only difference from calling ReconcileWith in a loop: the same
// records, the same eligibility rule, the same expected-revision CAS on the
// same revision, the same publication authority. A Query Group the batch did
// not find is placed, exactly as an absent record was placed before.
//
// The batch read failing fails the round before anything is published. It has
// to: the records say who currently holds each Query Group, and a round that
// treated "could not read" as "nobody holds it" would rendezvous the entire
// population onto fresh owners in one go.
func (reconciler *Reconciler) ReconcileRound(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	queryGroups []execution.QueryGroupIdentity,
	workers []ownership.WorkerRegistration,
	at time.Time,
) (map[execution.QueryGroupIdentity]ownership.AssignmentRecord, ownership.ControlReadStats, error) {
	return reconciler.ReconcileRoundWithScopes(ctx, authority, queryGroups, workers, at, ContentScopes{})
}

// ReconcileRoundWithScopes is ReconcileRound with a content scope policy:
// the same placement decisions, and beside them, for a record whose
// desired worker stays, the scope the policy asks for (decision-016). A
// scope publish is a decision like a placement -- same authority, same
// expected-revision CAS on the record just read -- and it is the only
// reason an eligible incumbent's record is written.
func (reconciler *Reconciler) ReconcileRoundWithScopes(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	queryGroups []execution.QueryGroupIdentity,
	workers []ownership.WorkerRegistration,
	at time.Time,
	scopes ContentScopes,
) (map[execution.QueryGroupIdentity]ownership.AssignmentRecord, ownership.ControlReadStats, error) {
	settlement, err := reconciler.ReconcileRoundSettling(ctx, authority, queryGroups, workers, at, scopes)
	return settlement.Records, settlement.Stats, err
}

// RoundSettlement is one round's records and what the round did to get
// them beyond keeping and placing.
type RoundSettlement struct {
	Records map[execution.QueryGroupIdentity]ownership.AssignmentRecord
	Stats   ownership.ControlReadStats
	// Replaced counts the Query Groups whose holder is alive and READY but no
	// longer eligible - a rollout that changed the capability digest - that
	// the round moved, each a handover; Deferred those it left where they
	// were for a later round, because the round's handover batch was spent.
	Replaced, Deferred int
	// Unplaceable counts the Query Groups no ready worker can take this
	// round. Each keeps the record it had, or stays without one; the round
	// goes on with the rest.
	Unplaceable int
	// DeclinedEverywhere is the part of Unplaceable that is unplaceable
	// because every worker that could take it declines it - its execution
	// hung on each in turn, the Query Group's own bug - with, for the first
	// few, how many workers declined it and the stage each hung in.
	DeclinedEverywhere       int
	DeclinedEverywhereSample []DeclinedEverywhere
}

// MaxDeclinedEverywhereSamples bounds the Query Groups a round names.
const MaxDeclinedEverywhereSamples = 8

// DeclinedEverywhere is one Query Group every eligible worker declines.
type DeclinedEverywhere struct {
	QueryGroup string `json:"query_group"`
	// Hops is how many workers declined it: how far it walked the fleet.
	Hops int `json:"hops"`
	// Stages is each decliner and the stage its execution hung in.
	Stages []DeclinerStage `json:"stages,omitempty"`
}

// DeclinerStage is one worker declining a Query Group and the stage its
// execution of it hung in.
type DeclinerStage struct {
	Worker string `json:"worker"`
	Stage  string `json:"stage,omitempty"`
}

// ReconcileRoundSettling is ReconcileRoundWithScopes with the round's
// settlement. One round moves at most a batch (rebalanceBatch) of Query
// Groups off holders that are alive and READY but ineligible: those are
// handovers, each holder drains the Slot it is running, and a rollout that
// changes the capability digest makes every old replica's Query Group one
// at once (design 02 §6.2: handovers go in batches; a membership change does
// not move everything). The batch is shared with the count correction that
// follows in the same round (PlanRebalanceWithin), so a round hands over at
// most one batch in all. A holder that is gone, not ready or draining is
// not a handover - nothing is left there to drain - and its Query Groups
// are placed at once.
func (reconciler *Reconciler) ReconcileRoundSettling(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	queryGroups []execution.QueryGroupIdentity,
	workers []ownership.WorkerRegistration,
	at time.Time,
	scopes ContentScopes,
) (RoundSettlement, error) {
	if reconciler == nil {
		return RoundSettlement{}, errors.New("alarmd scheduler: initialized reconciler is required")
	}
	current, stats, err := reconciler.store.ReadAssignments(ctx, queryGroups)
	settlement := RoundSettlement{Stats: stats}
	if err != nil {
		return settlement, err
	}
	index := indexReadyWorkers(workers)
	budget := rebalanceBatch(len(queryGroups))
	settled := make(map[execution.QueryGroupIdentity]ownership.AssignmentRecord, len(queryGroups))
	for _, queryGroup := range queryGroups {
		existing, hasCurrent := current[queryGroup]
		record, outcome, settleErr := reconciler.settle(ctx, authority, queryGroup, existing, hasCurrent, index, at, scopes, &budget)
		if settleErr != nil {
			return settlement, settleErr
		}
		switch outcome {
		case settleReplaced:
			settlement.Replaced++
		case settleDeferred:
			settlement.Deferred++
		case settleUnplaceable, settleDeclinedEverywhere:
			settlement.Unplaceable++
			if outcome == settleDeclinedEverywhere {
				settlement.DeclinedEverywhere++
				if len(settlement.DeclinedEverywhereSample) < MaxDeclinedEverywhereSamples {
					settlement.DeclinedEverywhereSample = append(settlement.DeclinedEverywhereSample, index.declinedBy(queryGroup))
				}
			}
			if !hasCurrent {
				continue
			}
		}
		settled[queryGroup] = record
	}
	settlement.Records = settled
	return settlement, nil
}

// settleOutcome is what settle did with one Query Group.
type settleOutcome int

const (
	// settleKept: the holder is eligible; the record stands, or carries the
	// round's content scope or timeline revision.
	settleKept settleOutcome = iota
	// settlePlaced: no record, or a holder that is not alive and READY.
	settlePlaced
	// settleReplaced: a live, ineligible holder handed over within the batch.
	settleReplaced
	// settleDeferred: a live, ineligible holder kept for a later batch.
	settleDeferred
	// settleUnplaceable: no ready worker can take it; kept as it was.
	settleUnplaceable
	// settleDeclinedEverywhere: unplaceable because every worker that could
	// take it declines it.
	settleDeclinedEverywhere
)

// settle is the placement decision itself, over a record the caller has
// already read. Both entry points route through it so that reading one record
// or reading a batch of them cannot come to different conclusions. budget is
// the round's remaining handover batch; nil is no batch, for the one-Query
// Group entry point.
func (reconciler *Reconciler) settle(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	queryGroup execution.QueryGroupIdentity,
	current ownership.AssignmentRecord,
	hasCurrent bool,
	workers readyWorkerIndex,
	at time.Time,
	scopes ContentScopes,
	budget *int,
) (ownership.AssignmentRecord, settleOutcome, error) {
	expectedRevision := uint64(0)
	if hasCurrent {
		expectedRevision = current.RecordRevision
	}
	timeline := reconciler.timelineRevisionFor(ctx, queryGroup, current, hasCurrent)
	if hasCurrent && reconciler.router.incumbentEligibleIn(queryGroup, current.DesiredWorkerID, workers, at) {
		scope, withdraw, needed := scopes.wanted(queryGroup, current)
		if !needed && timeline == 0 {
			return current, settleKept, nil
		}
		record, err := reconciler.store.PublishAssignment(
			ctx, authority, ownership.AssignmentDecision{
				QueryGroup: queryGroup, DesiredWorkerID: current.DesiredWorkerID,
				ExpectedRecordRevision: expectedRevision, PlacementReason: current.PlacementReason, DecidedAt: at,
				ContentScope: scope, WithdrawContentScope: withdraw, TimelineRecordRevision: timeline,
			},
		)
		return record, settleKept, err
	}
	selected, err := reconciler.router.Select(queryGroup, workers.ordered, at)
	if errors.Is(err, ErrDeclinedEverywhere) {
		return current, settleDeclinedEverywhere, nil
	}
	if errors.Is(err, ErrNoEligibleWorker) {
		// Nobody can take it this round; one Query Group's answer is its
		// own (design 016 §4.1), and the rest of the round goes on.
		return current, settleUnplaceable, nil
	}
	if err != nil {
		return ownership.AssignmentRecord{}, settlePlaced, err
	}
	outcome := settlePlaced
	if hasCurrent && workers.alive(queryGroup, current.DesiredWorkerID, at) {
		if budget != nil && *budget <= 0 {
			return current, settleDeferred, nil
		}
		if budget != nil {
			*budget--
		}
		outcome = settleReplaced
	}
	decision := ownership.AssignmentDecision{
		QueryGroup: queryGroup, DesiredWorkerID: selected.WorkerID,
		ExpectedRecordRevision: expectedRevision, PlacementReason: ownership.PlacementRendezvous, DecidedAt: at,
		TimelineRecordRevision: timeline,
	}
	// A placement carries the content it places onto when the round
	// declares scopes, so a Query Group is never placed without one and
	// then named on the next round; and it withdraws when the round
	// withdraws, so a move never carries a stale scope along.
	switch scopes.Policy {
	case ContentScopesDeclared:
		decision.ContentScope = scopes.Digests[queryGroup]
	case ContentScopesWithdrawn:
		decision.WithdrawContentScope = true
	}
	record, err := reconciler.store.PublishAssignment(ctx, authority, decision)
	return record, outcome, err
}
