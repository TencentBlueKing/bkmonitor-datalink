package controlplane

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
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
		return previous, nil
	}
	if publication.PublicationEpoch < previous.Current.PublicationEpoch {
		return previous, nil
	}
	if publication.PublicationEpoch == previous.Current.PublicationEpoch {
		return ActivationState{}, ErrActivationEpochCollision
	}
	failureStage, failureClass = ActivationFailureStageCandidateLoad, ActivationFailureClassDependencyIO
	snapshot, err := reconciler.repository.LoadPublishedSnapshot(ctx, publication)
	if err != nil {
		return ActivationState{}, err
	}
	failureStage, failureClass = ActivationFailureStageCompile, ActivationFailureClassOther
	boundary := execution.EvaluationTime(reconciler.now().Unix())
	if boundary <= 0 {
		return ActivationState{}, errors.New("alarmd controlplane: Schedule activation clock must produce a positive Unix second")
	}
	failureClass = ActivationFailureClassCorrupt
	newGroups, err := queryGroupMap(snapshot.QueryGroups)
	if err != nil {
		return ActivationState{}, err
	}
	failureStage, failureClass = ActivationFailureStageCurrentRecovery, ActivationFailureClassProjectionConflict
	oldSnapshot, err := reconciler.repository.LoadPublishedSnapshot(ctx, previous.Current)
	var oldGroups map[execution.QueryGroupIdentity]QueryGroup
	if errors.Is(err, ErrSnapshotUnavailable) {
		if previous.SchemaVersion == activationSchemaVersion {
			identities, loadErr := reconciler.repository.LoadActiveQueryGroupSet(ctx, previous.ActiveQGSetRef)
			if loadErr != nil {
				return ActivationState{}, loadErr
			}
			candidates := make(map[execution.QueryGroupIdentity]QueryGroup, len(identities))
			for _, identity := range identities {
				candidates[identity] = QueryGroup{Identity: identity}
			}
			oldGroups, err = reconciler.repository.loadActivatedGroupsFromOpenSchedules(ctx, previous, candidates)
		} else {
			oldGroups, err = reconciler.repository.loadActivatedGroupsFromScheduleScan(ctx, previous)
		}
	} else if err == nil {
		oldGroups, err = queryGroupMap(oldSnapshot.QueryGroups)
	}
	if err != nil {
		return ActivationState{}, err
	}
	failureStage, failureClass = ActivationFailureStageReactivation, ActivationFailureClassDependencyIO
	failureCounts = activationReconciliationCounts(previous.Draining, newGroups)
	reactivating, err := reconciler.reactivatingQueryGroups(ctx, previous.Draining, newGroups, boundary)
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
	records, _, err := compilePublishedActivation(ctx, reconciler.compiler, reconciler.stateSemantics, snapshot, boundary)
	if err != nil {
		return ActivationState{}, err
	}
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
	if len(reactivating) > 0 {
		planGroups := make(map[execution.PlanIdentity]execution.QueryGroupIdentity)
		for _, group := range snapshot.QueryGroups {
			for _, plan := range group.Plans {
				planGroups[plan.Identity] = group.Identity
			}
		}
		for index := range records {
			if _, reactivated := reactivating[planGroups[records[index].Fact.Plan]]; reactivated {
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

	snapshot, err := reconciler.repository.LoadPublishedSnapshot(ctx, previous.Current)
	var groups map[execution.QueryGroupIdentity]QueryGroup
	if errors.Is(err, ErrSnapshotUnavailable) {
		groups, err = reconciler.repository.loadActivatedGroupsFromScheduleScan(ctx, previous)
	} else if err == nil {
		groups, err = queryGroupMap(snapshot.QueryGroups)
	}
	if err != nil {
		return ActivationState{}, err
	}
	identities := make([]execution.QueryGroupIdentity, 0, len(groups))
	covered := make(map[execution.PlanIdentity]struct{}, len(previous.Plans))
	for identity := range groups {
		timeline, _, loadErr := reconciler.repository.loadScheduleTimeline(ctx, identity)
		if loadErr != nil {
			return ActivationState{}, loadErr
		}
		if timeline.RetiredAt != nil || len(timeline.Segments) == 0 {
			return ActivationState{}, ErrSnapshotUnavailable
		}
		open := timeline.Segments[len(timeline.Segments)-1]
		if open.Schedule.Segment.End != nil || open.Schedule.Segment.Publication.SnapshotRevision != previous.Current.SnapshotRevision || uint64(open.Schedule.Segment.Publication.PublicationEpoch) != previous.Current.PublicationEpoch {
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

func (reconciler *ScheduleActivationReconciler) reactivatingQueryGroups(
	ctx context.Context,
	draining []DrainingQueryGroup,
	newGroups map[execution.QueryGroupIdentity]QueryGroup,
	boundary execution.EvaluationTime,
) (map[execution.QueryGroupIdentity]struct{}, error) {
	result := make(map[execution.QueryGroupIdentity]struct{})
	for _, projection := range draining {
		if _, reappeared := newGroups[projection.QueryGroup]; !reappeared {
			continue
		}
		reactivatable, err := reconciler.repository.drainingReactivatable(ctx, projection, boundary, reconciler.progress)
		if err != nil {
			return nil, err
		}
		if !reactivatable {
			return nil, ErrReactivationNotDrained
		}
		result[projection.QueryGroup] = struct{}{}
	}
	return result, nil
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
