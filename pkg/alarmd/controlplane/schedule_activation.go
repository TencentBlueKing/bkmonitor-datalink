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
	now            func() time.Time
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
	if publication.PublicationEpoch <= previous.Current.PublicationEpoch {
		return ActivationState{}, errors.New("alarmd controlplane: Schedule activation publication must advance")
	}
	snapshot, err := reconciler.repository.LoadSnapshot(ctx, publication.SnapshotRevision)
	if err != nil {
		return ActivationState{}, err
	}
	if snapshot.Publication != publication {
		return ActivationState{}, errors.New("alarmd controlplane: Schedule activation requires the confirmed publication")
	}
	boundary := execution.EvaluationTime(reconciler.now().Unix())
	if boundary <= 0 {
		return ActivationState{}, errors.New("alarmd controlplane: Schedule activation clock must produce a positive Unix second")
	}
	records, _, err := compilePublishedActivation(ctx, reconciler.compiler, reconciler.stateSemantics, snapshot, boundary)
	if err != nil {
		return ActivationState{}, err
	}
	oldSnapshot, err := reconciler.repository.LoadSnapshot(ctx, previous.Current.SnapshotRevision)
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
	draining, err := expectedDrainingProjection(previous.Draining, oldGroups, newGroups, boundary)
	if err != nil {
		return ActivationState{}, err
	}
	sort.Slice(records, func(i, j int) bool { return lessPlanIdentity(records[i].Fact.Plan, records[j].Fact.Plan) })
	next := ActivationState{RecordRevision: previous.RecordRevision + 1, Current: publication,
		Plans: records, Draining: draining}
	expected := ActivationExpectation{RecordRevision: previous.RecordRevision, Current: previous.Current, Pending: previous.Pending}
	if applyErr := reconciler.repository.CompareAndSetPublicationScheduleActivation(ctx, expected, next, boundary); applyErr != nil {
		winner, loadErr := reconciler.repository.LoadActivation(ctx)
		if loadErr == nil && winner.Current.PublicationEpoch >= publication.PublicationEpoch {
			return winner, nil
		}
		if errors.Is(applyErr, ErrActivationConflict) && loadErr != nil {
			return ActivationState{}, loadErr
		}
		return ActivationState{}, applyErr
	}
	return reconciler.repository.LoadActivation(ctx)
}
