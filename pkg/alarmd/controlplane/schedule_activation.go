package controlplane

import (
	"context"
	"errors"
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
}

type ScheduleActivationProgressReader interface {
	LoadProgress(context.Context, execution.ProgressIdentity) (execution.ProgressLoadResult, error)
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
	previous, err := reconciler.repository.LoadActivation(ctx)
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
		upgraded, upgradeErr := reconciler.upgradeLegacyActivation(ctx, previous)
		if upgradeErr != nil {
			return ActivationState{}, upgradeErr
		}
		if upgraded.Current == publication {
			return upgraded, nil
		}
		return reconciler.Ensure(ctx, publication)
	}
	if previous.Current == publication {
		failureStage, failureClass = ActivationFailureStageCurrentRecovery, ActivationFailureClassProjectionConflict
		if _, loadErr := reconciler.repository.LoadActiveQueryGroupSet(ctx, previous.ActiveQGSetRef); loadErr != nil {
			return ActivationState{}, loadErr
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
	for index := range records {
		previousRecord, continuouslyActive := previousRecords[records[index].Fact.Plan]
		if !continuouslyActive ||
			previousRecord.Fact.Selected.StateGeneration != records[index].Fact.Selected.StateGeneration {
			records[index].Fact.Selected.ForceWarming = true
		}
	}
	// A Query Group that reopens a retired timeline restarts every Plan it
	// carries through WARMING, including a Plan that stayed active under
	// other Query Groups in between and so is not caught above. The set is
	// read from the persisted timelines rather than from the Draining
	// projection, which forgets a drained Query Group before its timeline
	// expires; the CAS side appends to the same timelines.
	if len(returning) > 0 {
		planGroups := make(map[execution.PlanIdentity]execution.QueryGroupIdentity)
		for identity, group := range newGroups {
			for _, plan := range group.Plans {
				planGroups[plan.Identity] = identity
			}
		}
		for index := range records {
			if _, returned := returning[planGroups[records[index].Fact.Plan]]; returned {
				records[index].Fact.Selected.ForceWarming = true
			}
		}
	}
	sort.Slice(records, func(i, j int) bool { return lessPlanIdentity(records[i].Fact.Plan, records[j].Fact.Plan) })
	next := ActivationState{RecordRevision: previous.RecordRevision + 1, Current: publication,
		Plans: records, Draining: draining}
	expected := ActivationExpectation{RecordRevision: previous.RecordRevision, Current: previous.Current, Pending: previous.Pending}
	failureStage, failureClass = ActivationFailureStageScheduleCutover, ActivationFailureClassScheduleConflict
	if applyErr := reconciler.repository.CompareAndSetPublicationScheduleActivation(
		ctx, expected, next, boundary, reconciler.progress,
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

func (reconciler *ScheduleActivationReconciler) upgradeLegacyActivation(
	ctx context.Context,
	previous ActivationState,
) (state ActivationState, err error) {
	failureStage := ActivationFailureStageCurrentRecovery
	failureClass := ActivationFailureClassProjectionConflict
	defer func() { err = wrapActivationFailure(failureStage, failureClass, err) }()

	published, err := reconciler.repository.loadPublishedGroups(ctx, previous.Current)
	var groups map[execution.QueryGroupIdentity]QueryGroup
	if errors.Is(err, ErrSnapshotUnavailable) {
		groups, err = reconciler.repository.loadActivatedGroupsFromScheduleScan(ctx, previous)
	} else if err == nil {
		groups = published.groups
	}
	if err != nil {
		return ActivationState{}, err
	}
	identities := make([]execution.QueryGroupIdentity, 0, len(groups))
	covered := make(map[execution.PlanIdentity]struct{}, len(previous.Plans))
	for identity := range groups {
		timeline, loadErr := reconciler.repository.loadScheduleTimeline(ctx, identity)
		if loadErr != nil {
			return ActivationState{}, loadErr
		}
		if timeline.RetiredAt != nil || len(timeline.Segments) == 0 {
			return ActivationState{}, ErrSnapshotUnavailable
		}
		open := timeline.Segments[len(timeline.Segments)-1]
		if open.Schedule.Segment.End != nil {
			return ActivationState{}, ErrSnapshotUnavailable
		}
		if err := validateOpenSegmentActivation(previous, open); err != nil {
			failureClass = ActivationFailureClassCoverageConflict
			return ActivationState{}, err
		}
		for _, record := range open.Plans {
			if _, duplicate := covered[record.Fact.Plan]; duplicate {
				failureClass = ActivationFailureClassCoverageConflict
				return ActivationState{}, ErrSnapshotUnavailable
			}
			covered[record.Fact.Plan] = struct{}{}
		}
		identities = append(identities, identity)
	}
	if len(covered) != len(previous.Plans) {
		failureClass = ActivationFailureClassCoverageConflict
		return ActivationState{}, ErrSnapshotUnavailable
	}
	next := previous
	next.RecordRevision++
	next.SchemaVersion = activationSchemaVersion
	expected := ActivationExpectation{RecordRevision: previous.RecordRevision, Current: previous.Current, Pending: previous.Pending}
	// persistCutoverActivation with no timeline updates performs the v1->v2
	// same-publication CAS and leaves Current/Pending/Schedule unchanged.
	failureStage, failureClass = ActivationFailureStagePersist, ActivationFailureClassProjectionConflict
	ref, payload, err := reconciler.repository.persistAndVerifyActiveQGSet(ctx, identities)
	if err != nil {
		return ActivationState{}, err
	}
	next.ActiveQGSetRef = ref
	if err := reconciler.repository.persistActivationRefUpgrade(ctx, expected, next, payload); err != nil {
		winner, loadErr := reconciler.repository.LoadActivation(ctx)
		if loadErr == nil && winner.SchemaVersion == activationSchemaVersion {
			return winner, nil
		}
		failureClass = ActivationFailureClassCASConflict
		return ActivationState{}, err
	}
	failureClass = ActivationFailureClassDependencyIO
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
	returningPlans := make(map[execution.PlanIdentity]struct{})
	for identity := range reactivating {
		for _, plan := range groups[identity].Plans {
			returningPlans[plan.Identity] = struct{}{}
		}
	}
	next := previous
	next.RecordRevision = previous.RecordRevision + 1
	next.Plans = append([]PlanActivationRecord(nil), previous.Plans...)
	for _, record := range compiled {
		if _, returning := returningPlans[record.Fact.Plan]; !returning {
			continue
		}
		record.Fact.Selected.ForceWarming = true
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
