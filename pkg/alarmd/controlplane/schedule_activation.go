package controlplane

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// ScheduleActivationReconciler is called only by the current Control Leader.
// It turns each confirmed publication into one atomic activation and Schedule
// timeline transition. Workers only read the persisted winner.
type ScheduleActivationReconciler struct {
	repository     *RedisCatalogRepository
	compiler       RuntimePlanCompiler
	stateSemantics strategy.StateSemantics
	progress       ScheduleActivationProgressReader
	now            func() time.Time
	// scopes, when set, is told which Query Groups change content and to
	// what before the cutover is written (decision-016 batch 3). See
	// ContentScopeWriter and WithContentScopeWriter.
	scopes ContentScopeWriter
	// repairs, when set, names the Query Groups the Workers report meeting
	// an unreadable timeline for; the reconciler reads each again and
	// rewrites the ones it cannot decode either. See WithTimelineRepairs.
	repairs TimelineRepairSource
}

type ScheduleActivationProgressReader interface {
	LoadProgress(context.Context, execution.ProgressIdentity) (execution.ProgressLoadResult, error)
}

// ContentScopeWriter writes the content each changing Query Group is about
// to be published with into its Assignment record, before the Segment that
// carries that content is cut. A Slot frozen from the new Segment declares
// the new digest; if the record still named only the old one the fence would
// refuse it (CONTENT_MOVED) until the next reconcile round caught up, which
// is one round's worth of refused Slots on every publication. Written first,
// the record already names the new content (pending under a live lease, and
// the fence admits the pending scope) by the time any Segment says it.
//
// It is advisory to the cutover: an error is reported by the implementation
// and the activation proceeds, because the reconcile round writes the same
// scopes within one round and a publication must not fail for it. What
// counts as declaring, and whether the fleet takes part at all, is the
// implementation's to decide -- it is the same gate the reconcile round
// uses, so the two never disagree on whether a scope is written.
type ContentScopeWriter interface {
	PublishContentScopes(context.Context, map[execution.QueryGroupIdentity]execution.ObjectDigest)
}

// WithContentScopeWriter attaches the writer the cutover tells first.
func (reconciler *ScheduleActivationReconciler) WithContentScopeWriter(writer ContentScopeWriter) *ScheduleActivationReconciler {
	if reconciler != nil {
		reconciler.scopes = writer
	}
	return reconciler
}

// contentChanges is what the new publication says about each Query Group
// whose content differs from the previous activation's, or which the
// previous activation did not have: the digest its Segment is about to
// carry. Unchanged Query Groups are left out; their records already name it.
func contentChanges(published PublishedContent, previous map[execution.QueryGroupIdentity]execution.ObjectDigest) map[execution.QueryGroupIdentity]execution.ObjectDigest {
	changes := make(map[execution.QueryGroupIdentity]execution.ObjectDigest)
	for identity, entry := range published.Groups {
		if entry.Digest == "" {
			continue
		}
		if old, had := previous[identity]; had && old == entry.Digest {
			continue
		}
		changes[identity] = entry.Digest
	}
	return changes
}

const maxReappearedQueryGroupFailureSamples = 8

func NewScheduleActivationReconciler(
	repository *RedisCatalogRepository,
	compiler RuntimePlanCompiler,
	stateSemantics strategy.StateSemantics,
	now func() time.Time,
) (*ScheduleActivationReconciler, error) {
	if repository == nil || compiler == nil || now == nil || !validStateSemantics(stateSemantics) {
		return nil, errors.New("alarmd controlplane: invalid Schedule activation reconciler")
	}
	return &ScheduleActivationReconciler{repository: repository, compiler: compiler,
		stateSemantics: stateSemantics, now: now}, nil
}

func NewScheduleActivationReconcilerWithProgress(
	repository *RedisCatalogRepository,
	compiler RuntimePlanCompiler,
	stateSemantics strategy.StateSemantics,
	progress ScheduleActivationProgressReader,
	now func() time.Time,
) (*ScheduleActivationReconciler, error) {
	reconciler, err := NewScheduleActivationReconciler(repository, compiler, stateSemantics, now)
	if err != nil {
		return nil, err
	}
	if progress == nil {
		return nil, errors.New("alarmd controlplane: Schedule activation Progress reader is required")
	}
	reconciler.progress = progress
	return reconciler, nil
}

func (reconciler *ScheduleActivationReconciler) Ensure(
	ctx context.Context,
	publication SnapshotPublicationRef,
) (state ActivationState, err error) {
	failureStage := ActivationFailureStageActivationLoad
	failureClass := ActivationFailureClassOther
	failureCounts := ActivationFailure{}
	defer func() { err = wrapActivationFailure(failureStage, failureClass, err, failureCounts) }()

	if reconciler == nil || reconciler.repository == nil || reconciler.compiler == nil || reconciler.now == nil || publication.validate() != nil {
		return ActivationState{}, errors.New("alarmd controlplane: valid Schedule activation publication is required")
	}
	failureClass = ActivationFailureClassDependencyIO
	// A body without its header reads as an activation to every reader, and
	// every guarded write compares against the header: without it each
	// cutover below is refused on every round. It is written back first, as
	// the body describes it; with both gone this does nothing and the first
	// activation below takes over, as before.
	if _, err := reconciler.repository.RebuildActivationHeader(ctx); err != nil {
		return ActivationState{}, err
	}
	previous, err := reconciler.repository.LoadActivation(ctx)
	if errors.Is(err, ErrActivationBodyMissing) {
		// The header is here without its body. The first activation refuses
		// any header, so taking that path left every round refused for as
		// long as the header stayed - it has no TTL. The body is recovered
		// instead, then this round goes on as it would have.
		outcome, rebuildErr := reconciler.repository.RebuildActivationBody(ctx)
		if outcome != "" {
			reconciler.repository.rebuilds.add(outcome)
		}
		if rebuildErr != nil {
			return ActivationState{}, rebuildErr
		}
		previous, err = reconciler.repository.LoadActivation(ctx)
	}
	if errors.Is(err, ErrActivationUnavailable) {
		failureStage, failureClass = ActivationFailureStageCompile, ActivationFailureClassOther
		initial, buildErr := NewInitialScheduleActivator(
			reconciler.repository, reconciler.compiler, reconciler.stateSemantics, reconciler.now,
		)
		if buildErr != nil {
			return ActivationState{}, buildErr
		}
		return initial.Ensure(ctx, publication)
	}
	if err != nil {
		return ActivationState{}, err
	}
	if previous.SchemaVersion != activationSchemaVersion {
		failureStage, failureClass = ActivationFailureStageCurrentRecovery, ActivationFailureClassProjectionConflict
		return ActivationState{}, fmt.Errorf("alarmd controlplane: activation schema %q is not one this build activates from", previous.SchemaVersion)
	}
	reported := reconciler.reportedTimelines()
	// A Query Group the activation read left out because its timeline did
	// not decode is named as the Workers name theirs: its records are not in
	// previous.Plans, which says it is unread, never that it left. Under the
	// current publication only the undecodable ones are rebuilt and
	// rewritten below; a cutover to a new publication reads every one of
	// them and rewrites only the undecodable ones, leaving one another
	// schema wrote as it is.
	undecodable := undecodableSkipped(previous.SkippedTimelines)
	if len(previous.SkippedTimelines) > 0 {
		merged := make(map[execution.QueryGroupIdentity]struct{}, len(reported)+len(previous.SkippedTimelines))
		for queryGroup := range reported {
			merged[queryGroup] = struct{}{}
		}
		for _, skipped := range previous.SkippedTimelines {
			merged[skipped.QueryGroup] = struct{}{}
		}
		reported = merged
	}
	if previous.Current == publication {
		failureStage, failureClass = ActivationFailureStageCurrentRecovery, ActivationFailureClassProjectionConflict
		active, loadErr := reconciler.repository.LoadActiveQueryGroupSet(ctx, previous.ActiveQGSetRef)
		if loadErr != nil {
			return ActivationState{}, loadErr
		}
		// A reported unreadable timeline is looked at first, on the current
		// publication: without a new one nothing else reads it again. The
		// held reactivation below then starts from whatever this wrote; the
		// two are separate writes because they answer separate facts, and a
		// round has both only while both are pending.
		repairing := withoutNewerFormat(reported, previous.SkippedTimelines)
		if len(repairing) > 0 {
			failureStage, failureClass = ActivationFailureStageCurrentRecovery, ActivationFailureClassDependencyIO
			boundary := execution.EvaluationTime(reconciler.now().Unix())
			if boundary <= 0 {
				return ActivationState{}, errors.New("alarmd controlplane: Schedule activation clock must produce a positive Unix second")
			}
			candidate, rebuildErr := reconciler.rebuildSkippedRecords(ctx, previous, active, undecodable, boundary)
			if rebuildErr != nil {
				return ActivationState{}, rebuildErr
			}
			if _, repairErr := reconciler.repository.RepairUnreadableTimelines(ctx, candidate, active, repairing, boundary); repairErr != nil {
				return ActivationState{}, repairErr
			}
			// Whatever the repair did - wrote, lost to another writer, or
			// found nothing to write - the activation is read back, so the
			// reactivation is fenced on the one that is there.
			if previous, err = reconciler.repository.LoadActivation(ctx); err != nil {
				return ActivationState{}, err
			}
			if previous.Current != publication {
				return previous, nil
			}
		}
		if len(previous.Draining) == 0 {
			return previous, nil
		}
		failureStage, failureClass = ActivationFailureStageReactivation, ActivationFailureClassDependencyIO
		return reconciler.reactivateHeld(ctx, previous)
	}
	if publication.PublicationEpoch < previous.Current.PublicationEpoch {
		return previous, nil
	}
	if publication.PublicationEpoch == previous.Current.PublicationEpoch {
		return ActivationState{}, ErrActivationEpochCollision
	}
	failureStage, failureClass = ActivationFailureStageCandidateLoad, ActivationFailureClassDependencyIO
	published, err := reconciler.repository.loadPublishedGroups(ctx, publication)
	if err != nil {
		return ActivationState{}, err
	}
	failureStage, failureClass = ActivationFailureStageCompile, ActivationFailureClassOther
	boundary := execution.EvaluationTime(reconciler.now().Unix())
	if boundary <= 0 {
		return ActivationState{}, errors.New("alarmd controlplane: Schedule activation clock must produce a positive Unix second")
	}
	failureClass = ActivationFailureClassCorrupt
	newGroups := published.groups
	failureStage, failureClass = ActivationFailureStageCurrentRecovery, ActivationFailureClassProjectionConflict
	// The population the current activation runs comes from its manifest
	// when one is stored: the reconciler needs the identities, not the
	// content, and the manifest is a fraction of the Snapshot's size.
	previousContent, err := reconciler.repository.loadActivatedContent(ctx, previous)
	if err != nil {
		return ActivationState{}, err
	}
	oldGroups := previousContent.groups
	failureStage, failureClass = ActivationFailureStageReactivation, ActivationFailureClassDependencyIO
	failureCounts = activationReconciliationCounts(previous.Draining, newGroups)
	reactivating, err := reconciler.reactivatingQueryGroups(ctx, previous.Draining, newGroups, boundary)
	if err != nil {
		return ActivationState{}, err
	}
	returning, err := reconciler.repository.retiredQueryGroupsReturning(ctx, oldGroups, newGroups, boundary)
	if err != nil {
		return ActivationState{}, err
	}
	failureClass = ActivationFailureClassProjectionConflict
	draining, err := expectedDrainingProjection(
		previous.Draining, oldGroups, newGroups, reactivating, boundary,
		reconciler.repository.drainingRetirement(ctx, reconciler.progress, boundary),
	)
	if err != nil {
		return ActivationState{}, err
	}
	failureStage, failureClass = ActivationFailureStageCompile, ActivationFailureClassCorrupt
	// Query Groups whose content the previous activation already acted on
	// keep their records; only the rest are read from the object catalog
	// and compiled.
	records, compile, err := carriedActivationRecords(published, previous, previousContent, returning, reactivating)
	if err != nil {
		return ActivationState{}, err
	}
	failureClass = ActivationFailureClassDependencyIO
	if err := reconciler.repository.materialize(ctx, published, compile); err != nil {
		return ActivationState{}, err
	}
	failureClass = ActivationFailureClassCorrupt
	changed, err := published.loaded(compile)
	if err != nil {
		return ActivationState{}, err
	}
	compiled, _, err := compilePublishedGroups(ctx, reconciler.compiler, reconciler.stateSemantics, publication, changed,
		published.content.Groups, boundary)
	if err != nil {
		return ActivationState{}, err
	}
	records = append(records, compiled...)
	failureStage, failureClass = ActivationFailureStageCurrentRecovery, ActivationFailureClassCoverageConflict
	previousRecords, err := activationRecordMap(previous.Plans)
	if err != nil {
		return ActivationState{}, err
	}
	changedPlans := make(map[execution.PlanKey]changedPlan)
	for _, group := range changed {
		for _, plan := range group.Plans {
			changedPlans[plan.Key()] = changedPlan{plan: plan, group: group.Identity, dataset: group.QueryPlan.Normalization.DatasetContract}
		}
	}
	// A Query Group the activation read left out has no previous records, which
	// is not having left: when its content is the one it was activated with,
	// its Plans' generations are the ones it ran, and it is as continuously
	// active as any carried one - it does not warm again.
	unreadContinuing := make(map[execution.PlanKey]struct{})
	for _, skipped := range previous.SkippedTimelines {
		entry, remains := published.content.Groups[skipped.QueryGroup]
		if digest, known := previousContent.digests[skipped.QueryGroup]; !remains || !known || digest != entry.Digest {
			continue
		}
		for _, plan := range entry.Plans {
			unreadContinuing[plan] = struct{}{}
		}
	}
	carries := make(map[int]*execution.StateCarry)
	for index := range records {
		previousRecord, continuouslyActive := previousRecords[records[index].Fact.Key()]
		if _, unread := unreadContinuing[records[index].Fact.Key()]; unread && !continuouslyActive {
			previousRecord, continuouslyActive = records[index], true
		}
		if !continuouslyActive ||
			previousRecord.Fact.Selected.StateGeneration != records[index].Fact.Selected.StateGeneration {
			records[index].Fact.Selected.ForceWarming = true
		}
		// A generation that moved under a Plan that stayed active: what of
		// its state is still the same facts. See stateCarry.
		if continuouslyActive && previousRecord.Fact.Selected.StateGeneration != records[index].Fact.Selected.StateGeneration {
			current, compiledHere := changedPlans[records[index].Fact.Key()]
			if !compiledHere {
				reconciler.observeStateCarry(ctx, StateCarryPreviousUnreadable)
				continue
			}
			carry, outcome := reconciler.stateCarry(ctx, previousRecord, previousContent, current)
			if carry != nil {
				carries[index] = carry
				continue
			}
			reconciler.observeStateCarry(ctx, outcome)
		}
	}
	// A Query Group that reopens a retired timeline restarts every Plan it
	// carries through WARMING, including a Plan that stayed active under
	// other Query Groups in between and so is not caught above. The set is
	// read from the persisted timelines rather than from the Draining
	// projection, which forgets a drained Query Group before its timeline
	// expires; the CAS side appends to the same timelines.
	returned := make(map[int]struct{})
	if len(returning) > 0 {
		planGroups := make(map[execution.PlanKey]execution.QueryGroupIdentity)
		for identity, group := range newGroups {
			for _, plan := range group.Plans {
				planGroups[plan.Key()] = identity
			}
		}
		for index := range records {
			if _, back := returning[planGroups[records[index].Fact.Key()]]; back {
				records[index].Fact.Selected.ForceWarming = true
				returned[index] = struct{}{}
			}
		}
	}
	carried, discontinuous := applyStateCarries(records, carries, returned)
	for ; carried > 0; carried-- {
		reconciler.observeStateCarry(ctx, StateCarryCarried)
	}
	for ; discontinuous > 0; discontinuous-- {
		reconciler.observeStateCarry(ctx, StateCarryDiscontinuous)
	}
	sort.Slice(records, func(i, j int) bool { return lessPlanIdentity(records[i].Fact.Plan, records[j].Fact.Plan) })
	next := ActivationState{RecordRevision: previous.RecordRevision + 1, Current: publication,
		Plans: records, Draining: draining}
	expected := ActivationExpectation{RecordRevision: previous.RecordRevision, Current: previous.Current, Pending: previous.Pending}
	// The records first, the Segments second: by the time a worker freezes a
	// Slot from a Segment that names the new content, its Assignment record
	// already does.
	if reconciler.scopes != nil {
		if changes := contentChanges(published.content, previousContent.digests); len(changes) > 0 {
			reconciler.scopes.PublishContentScopes(ctx, changes)
		}
	}
	failureStage, failureClass = ActivationFailureStageScheduleCutover, ActivationFailureClassScheduleConflict
	if applyErr := reconciler.repository.compareAndSetPublicationScheduleActivation(
		ctx, expected, next, boundary, reconciler.progress, reported,
	); applyErr != nil {
		failureStage, failureClass = ActivationFailureStagePersist, ActivationFailureClassDependencyIO
		winner, loadErr := reconciler.repository.LoadActivation(ctx)
		if loadErr == nil {
			if winner.Current == publication || winner.Current.PublicationEpoch > publication.PublicationEpoch {
				return winner, nil
			}
			if winner.Current.PublicationEpoch == publication.PublicationEpoch {
				return ActivationState{}, ErrActivationEpochCollision
			}
		}
		if errors.Is(applyErr, ErrActivationConflict) && loadErr != nil {
			return ActivationState{}, loadErr
		}
		failureStage, failureClass = ActivationFailureStageScheduleCutover, ActivationFailureClassScheduleConflict
		return ActivationState{}, applyErr
	}
	failureStage, failureClass = ActivationFailureStagePersist, ActivationFailureClassDependencyIO
	return reconciler.repository.LoadActivation(ctx)
}

// reactivatingQueryGroups says which of the Draining Query Groups the
// publication brings back may be reactivated by this activation. The ones
// that have not drained are held: they stay in Draining, get no Segment and
// no activated Plans, and the activation goes ahead for everyone else. One
// undrained Query Group used to fail the whole activation, and on a
// deployment of any size there is nearly always one. A held Query Group
// comes back through reactivateHeld once it has drained.
func (reconciler *ScheduleActivationReconciler) reactivatingQueryGroups(
	ctx context.Context,
	draining []DrainingQueryGroup,
	newGroups map[execution.QueryGroupIdentity]QueryGroup,
	boundary execution.EvaluationTime,
) (map[execution.QueryGroupIdentity]struct{}, error) {
	reactivating, held, reappeared, err := reconciler.repository.partitionReactivations(ctx, draining, newGroups, boundary, reconciler.progress)
	if err != nil {
		return nil, err
	}
	reconciler.repository.observeActivationHold(ctx, reappeared, held, boundary)
	return reactivating, nil
}

// reactivateHeld runs on every reconcile of a publication that is already
// current and still lists Query Groups as draining. Those that are in the
// publication were held out of it; the ones that have drained since are
// brought back without a new publication: the record revision advances, they
// leave Draining, their Segment opens and their Plans join the activation
// restarting through WARMING. The held set is reported on every attempt, so
// the gauge follows it down to zero.
func (reconciler *ScheduleActivationReconciler) reactivateHeld(
	ctx context.Context,
	previous ActivationState,
) (ActivationState, error) {
	published, err := reconciler.repository.loadPublishedGroups(ctx, previous.Current)
	if err != nil {
		return ActivationState{}, err
	}
	groups := published.groups
	boundary := execution.EvaluationTime(reconciler.now().Unix())
	if boundary <= 0 {
		return ActivationState{}, errors.New("alarmd controlplane: Schedule activation clock must produce a positive Unix second")
	}
	reactivating, held, reappeared, err := reconciler.repository.partitionReactivations(ctx, previous.Draining, groups, boundary, reconciler.progress)
	if err != nil {
		return ActivationState{}, err
	}
	if reappeared == 0 {
		return previous, nil
	}
	reconciler.repository.observeActivationHold(ctx, reappeared, held, boundary)
	if len(reactivating) == 0 {
		return previous, nil
	}
	reactivated := make([]execution.QueryGroupIdentity, 0, len(reactivating))
	for identity := range reactivating {
		reactivated = append(reactivated, identity)
	}
	if err := reconciler.repository.materialize(ctx, published, reactivated); err != nil {
		return ActivationState{}, err
	}
	changed, err := published.loaded(reactivated)
	if err != nil {
		return ActivationState{}, err
	}
	compiled, _, err := compilePublishedGroups(ctx, reconciler.compiler, reconciler.stateSemantics, previous.Current, changed,
		published.content.Groups, boundary)
	if err != nil {
		return ActivationState{}, err
	}
	returningPlans := make(map[execution.PlanKey]struct{})
	for identity := range reactivating {
		for _, plan := range groups[identity].Plans {
			returningPlans[plan.Key()] = struct{}{}
		}
	}
	next := previous
	next.RecordRevision = previous.RecordRevision + 1
	next.Plans = append([]PlanActivationRecord(nil), previous.Plans...)
	for _, record := range compiled {
		if _, returning := returningPlans[record.Fact.Key()]; !returning {
			continue
		}
		record.Fact.Selected.ForceWarming = true
		// Held and back: the hold is a hole, so nothing is carried across it.
		record.Fact.Selected.Carry = nil
		next.Plans = append(next.Plans, record)
	}
	sort.Slice(next.Plans, func(i, j int) bool { return lessPlanIdentity(next.Plans[i].Fact.Plan, next.Plans[j].Fact.Plan) })
	next.Draining = make([]DrainingQueryGroup, 0, len(previous.Draining))
	for _, projection := range previous.Draining {
		if _, reactivated := reactivating[projection.QueryGroup]; reactivated {
			continue
		}
		next.Draining = append(next.Draining, projection)
	}
	expected := ActivationExpectation{RecordRevision: previous.RecordRevision, Current: previous.Current, Pending: previous.Pending}
	if err := reconciler.repository.CompareAndSetHeldReactivation(ctx, expected, next, reactivating, boundary, reconciler.progress); err != nil {
		winner, loadErr := reconciler.repository.LoadActivation(ctx)
		if loadErr == nil && winner.RecordRevision > previous.RecordRevision {
			return winner, nil
		}
		return ActivationState{}, err
	}
	return reconciler.repository.LoadActivation(ctx)
}

// observeActivationHold reports the reappearing Query Groups an activation
// attempt found, and which of them had not drained, as ActivationHoldFacts.
// It is emitted on every attempt that reached the check, with zero counts
// when nothing was held, so the gauge it feeds goes back to zero on its own.
func (repository *RedisCatalogRepository) observeActivationHold(
	ctx context.Context,
	reappeared int,
	held []DrainingQueryGroup,
	boundary execution.EvaluationTime,
) {
	facts := &observability.ActivationHoldFacts{Reappeared: reappeared, Held: len(held)}
	samples := make([]string, 0, len(held))
	for _, projection := range held {
		if age := int64(boundary - projection.RetiredBoundary); age > facts.MaxAgeSeconds {
			facts.MaxAgeSeconds = age
		}
		samples = append(samples, string(projection.QueryGroup))
	}
	sort.Strings(samples)
	if len(samples) > observability.MaxActivationHoldSamples {
		samples = samples[:observability.MaxActivationHoldSamples]
		facts.Truncated = true
	}
	facts.Samples = samples
	repository.observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageActivationHold,
		Result: observability.ResultSuccess, ActivationHold: facts,
	})
}

func activationReconciliationCounts(
	draining []DrainingQueryGroup,
	newGroups map[execution.QueryGroupIdentity]QueryGroup,
) ActivationFailure {
	reappeared := make([]execution.QueryGroupIdentity, 0)
	for _, projection := range draining {
		if _, exists := newGroups[projection.QueryGroup]; exists {
			reappeared = append(reappeared, projection.QueryGroup)
		}
	}
	sort.Slice(reappeared, func(i, j int) bool { return reappeared[i] < reappeared[j] })
	samples := reappeared
	truncated := len(samples) > maxReappearedQueryGroupFailureSamples
	if truncated {
		samples = samples[:maxReappearedQueryGroupFailureSamples]
	}
	return ActivationFailure{
		DrainingQueryGroups: len(draining), CandidateQueryGroups: len(newGroups),
		ReappearedQueryGroups:                len(reappeared),
		ReappearedQueryGroupSamples:          append([]execution.QueryGroupIdentity(nil), samples...),
		ReappearedQueryGroupSamplesTruncated: truncated,
	}
}

// withoutNewerFormat is named without the Query Groups the activation read
// left out because another schema wrote their timelines: the repair under
// the current publication would only read them again to leave them alone.
func withoutNewerFormat(named map[execution.QueryGroupIdentity]struct{}, skipped []SkippedTimeline) map[execution.QueryGroupIdentity]struct{} {
	var newer map[execution.QueryGroupIdentity]struct{}
	for _, entry := range skipped {
		if entry.Reason == SkippedTimelineNewerFormat {
			if newer == nil {
				newer = make(map[execution.QueryGroupIdentity]struct{}, len(skipped))
			}
			newer[entry.QueryGroup] = struct{}{}
		}
	}
	if len(newer) == 0 {
		return named
	}
	kept := make(map[execution.QueryGroupIdentity]struct{}, len(named))
	for queryGroup := range named {
		if _, isNewer := newer[queryGroup]; !isNewer {
			kept[queryGroup] = struct{}{}
		}
	}
	return kept
}

// rebuildSkippedRecords is previous with records for the active Query Groups
// of undecodable compiled again from the publication the activation runs:
// their records went with the bytes the activation read could not decode,
// and the repair that rewrites those timelines needs records to carry. Only
// those Query Groups' records are added; every other record is previous's.
//
// The rebuilt records are the lost ones in all but the epoch, and they do
// not warm again. The state generation is the compiled Plan's
// StateCompatibilityHash, and compiling refuses one that differs from the
// generation the publication stores for the Plan; the Query Group's content
// has not changed since it was activated, or a cutover would have rewritten
// the timeline. So the State on file is read on as before, and the event
// identity, which carries the generation, and the alert's dedupe, which does
// not, are the ones the Query Group already had: an ABNORMAL folds into the
// alert it had, and an open alert recovers from the next evaluation that
// finds the series normal. The epoch is the current publication's, which a
// carried record may have had older; a newer epoch only orders writes after
// the old ones. The Slots between the Worker's cursor and the rewrite are
// recorded as the rewrite's skip, as for a timeline whose records were read.
//
// A Query Group whose content cannot be compiled gets no records and is left
// to the repair, which counts it as failed and names it; the others go
// ahead. Reading the publication is the store's and fails the round as any
// read does.
func (reconciler *ScheduleActivationReconciler) rebuildSkippedRecords(
	ctx context.Context,
	previous ActivationState,
	active []execution.QueryGroupIdentity,
	undecodable map[execution.QueryGroupIdentity]struct{},
	boundary execution.EvaluationTime,
) (ActivationState, error) {
	if len(undecodable) == 0 {
		return previous, nil
	}
	published, err := reconciler.repository.loadPublishedGroups(ctx, previous.Current)
	if err != nil {
		return ActivationState{}, err
	}
	rebuilt := make([]execution.QueryGroupIdentity, 0, len(undecodable))
	for _, queryGroup := range active {
		if _, skipped := undecodable[queryGroup]; !skipped {
			continue
		}
		if _, published := published.groups[queryGroup]; published {
			rebuilt = append(rebuilt, queryGroup)
		}
	}
	if len(rebuilt) == 0 {
		return previous, nil
	}
	sort.Slice(rebuilt, func(i, j int) bool { return rebuilt[i] < rebuilt[j] })
	if err := reconciler.repository.materialize(ctx, published, rebuilt); err != nil {
		return ActivationState{}, err
	}
	candidate := previous
	candidate.Plans = append([]PlanActivationRecord(nil), previous.Plans...)
	for _, queryGroup := range rebuilt {
		groups, loadErr := published.loaded([]execution.QueryGroupIdentity{queryGroup})
		if loadErr != nil {
			reconciler.repository.observeTimelineRepairFailure(ctx, queryGroup, loadErr)
			continue
		}
		records, _, compileErr := compilePublishedGroups(ctx, reconciler.compiler, reconciler.stateSemantics, previous.Current,
			groups, published.content.Groups, boundary)
		if compileErr != nil {
			reconciler.repository.observeTimelineRepairFailure(ctx, queryGroup, compileErr)
			continue
		}
		candidate.Plans = append(candidate.Plans, records...)
	}
	sort.Slice(candidate.Plans, func(i, j int) bool {
		return lessPlanIdentity(candidate.Plans[i].Fact.Plan, candidate.Plans[j].Fact.Plan)
	})
	return candidate, nil
}
