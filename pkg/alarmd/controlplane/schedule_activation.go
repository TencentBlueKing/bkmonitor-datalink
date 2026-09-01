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
) (ActivationState, error) {
	if reconciler == nil || reconciler.repository == nil || reconciler.compiler == nil || reconciler.now == nil || publication.validate() != nil {
		return ActivationState{}, errors.New("alarmd controlplane: valid Schedule activation publication is required")
	}
	previous, err := reconciler.repository.LoadActivation(ctx)
	if errors.Is(err, ErrActivationUnavailable) {
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
	if previous.Current == publication {
		return previous, nil
	}
	if publication.PublicationEpoch < previous.Current.PublicationEpoch {
		return previous, nil
	}
	if publication.PublicationEpoch == previous.Current.PublicationEpoch {
		return ActivationState{}, errors.New("alarmd controlplane: Schedule activation publication epoch collision")
	}
	snapshot, err := reconciler.repository.LoadPublishedSnapshot(ctx, publication)
	if err != nil {
		return ActivationState{}, err
	}
	boundary := execution.EvaluationTime(reconciler.now().Unix())
	if boundary <= 0 {
		return ActivationState{}, errors.New("alarmd controlplane: Schedule activation clock must produce a positive Unix second")
	}
	oldSnapshot, err := reconciler.repository.LoadPublishedSnapshot(ctx, previous.Current)
	if err != nil {
		return ActivationState{}, err
	}
	oldGroups, err := queryGroupMap(oldSnapshot.QueryGroups)
	if err != nil {
		return ActivationState{}, err
	}
	newGroups, err := queryGroupMap(snapshot.QueryGroups)
	if err != nil {
		return ActivationState{}, err
	}
	reactivating, err := reconciler.reactivatingQueryGroups(ctx, previous.Draining, newGroups)
	if err != nil {
		return ActivationState{}, err
	}
	draining, err := expectedDrainingProjection(previous.Draining, oldGroups, newGroups, reactivating, boundary)
	if err != nil {
		return ActivationState{}, err
	}
	records, _, err := compilePublishedActivation(ctx, reconciler.compiler, reconciler.stateSemantics, snapshot, boundary)
	if err != nil {
		return ActivationState{}, err
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
	if applyErr := reconciler.repository.CompareAndSetPublicationScheduleActivation(
		ctx, expected, next, boundary, reconciler.progress,
	); applyErr != nil {
		winner, loadErr := reconciler.repository.LoadActivation(ctx)
		if loadErr == nil {
			if winner.Current == publication || winner.Current.PublicationEpoch > publication.PublicationEpoch {
				return winner, nil
			}
			if winner.Current.PublicationEpoch == publication.PublicationEpoch {
				return ActivationState{}, errors.New("alarmd controlplane: Schedule activation publication epoch collision")
			}
		}
		if errors.Is(applyErr, ErrActivationConflict) && loadErr != nil {
			return ActivationState{}, loadErr
		}
		return ActivationState{}, applyErr
	}
	return reconciler.repository.LoadActivation(ctx)
}

func (reconciler *ScheduleActivationReconciler) reactivatingQueryGroups(
	ctx context.Context,
	draining []DrainingQueryGroup,
	newGroups map[execution.QueryGroupIdentity]QueryGroup,
) (map[execution.QueryGroupIdentity]struct{}, error) {
	result := make(map[execution.QueryGroupIdentity]struct{})
	for _, projection := range draining {
		if _, reappeared := newGroups[projection.QueryGroup]; !reappeared {
			continue
		}
		if reconciler.progress == nil {
			return nil, errors.New("alarmd controlplane: reactivation requires the single Progress reader")
		}
		drained, err := reconciler.repository.queryGroupDrained(
			ctx, projection.QueryGroup, projection.RetiredBoundary, reconciler.progress,
		)
		if err != nil {
			return nil, err
		}
		if !drained {
			return nil, errors.New("alarmd controlplane: Query Group cannot reactivate before retirement drains")
		}
		result[projection.QueryGroup] = struct{}{}
	}
	return result, nil
}
