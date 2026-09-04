package controlplane

import (
	"context"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type ActivationFailureStage = observability.ActivationFailureStage

const (
	ActivationFailureStageActivationLoad  = observability.ActivationFailureStageActivationLoad
	ActivationFailureStageCandidateLoad   = observability.ActivationFailureStageCandidateLoad
	ActivationFailureStageCurrentRecovery = observability.ActivationFailureStageCurrentRecovery
	ActivationFailureStageReactivation    = observability.ActivationFailureStageReactivation
	ActivationFailureStageCompile         = observability.ActivationFailureStageCompile
	ActivationFailureStageScheduleCutover = observability.ActivationFailureStageScheduleCutover
	ActivationFailureStagePersist         = observability.ActivationFailureStagePersist
)

type ActivationFailureClass = observability.ActivationFailureClass

const (
	ActivationFailureClassUnavailable        = observability.ActivationFailureClassUnavailable
	ActivationFailureClassCorrupt            = observability.ActivationFailureClassCorrupt
	ActivationFailureClassEpochCollision     = observability.ActivationFailureClassEpochCollision
	ActivationFailureClassNotDrained         = observability.ActivationFailureClassNotDrained
	ActivationFailureClassProjectionConflict = observability.ActivationFailureClassProjectionConflict
	ActivationFailureClassScheduleConflict   = observability.ActivationFailureClassScheduleConflict
	ActivationFailureClassCoverageConflict   = observability.ActivationFailureClassCoverageConflict
	ActivationFailureClassCASConflict        = observability.ActivationFailureClassCASConflict
	ActivationFailureClassDependencyIO       = observability.ActivationFailureClassDependencyIO
	ActivationFailureClassOther              = observability.ActivationFailureClassOther
)

// ActivationFailure is bounded diagnostic context for one activation attempt.
// Counts are populated only for reactivation; identities and raw errors stay in
// the underlying error and never become metric labels.
type ActivationFailure struct {
	Stage                   ActivationFailureStage
	Class                   ActivationFailureClass
	DrainingQueryGroups     int
	CandidateQueryGroups    int
	ReactivatingQueryGroups int
}

type ActivationFailureError struct {
	Failure ActivationFailure
	Err     error
}

func (failure *ActivationFailureError) Error() string {
	if failure == nil || failure.Err == nil {
		return "alarmd controlplane: Schedule activation failed"
	}
	return failure.Err.Error()
}

func (failure *ActivationFailureError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}

func ActivationFailureFromError(err error) (ActivationFailure, bool) {
	var classified *ActivationFailureError
	if !errors.As(err, &classified) || classified == nil {
		return ActivationFailure{}, false
	}
	return classified.Failure, true
}

func wrapActivationFailure(
	stage ActivationFailureStage,
	fallback ActivationFailureClass,
	err error,
	counts ...ActivationFailure,
) error {
	if err == nil {
		return nil
	}
	if _, exists := ActivationFailureFromError(err); exists {
		return err
	}
	failure := ActivationFailure{Stage: stage, Class: classifyActivationFailure(err, fallback)}
	if stage == ActivationFailureStageReactivation && len(counts) > 0 {
		failure.DrainingQueryGroups = counts[0].DrainingQueryGroups
		failure.CandidateQueryGroups = counts[0].CandidateQueryGroups
		failure.ReactivatingQueryGroups = counts[0].ReactivatingQueryGroups
	}
	return &ActivationFailureError{Failure: failure, Err: err}
}

func classifyActivationFailure(err error, fallback ActivationFailureClass) ActivationFailureClass {
	switch {
	case errors.Is(err, ErrSnapshotUnavailable), errors.Is(err, ErrCatalogObjectUnavailable),
		errors.Is(err, ErrActivationUnavailable), errors.Is(err, ErrScheduleUnavailable):
		return ActivationFailureClassUnavailable
	case errors.Is(err, ErrActivationEpochCollision):
		return ActivationFailureClassEpochCollision
	case errors.Is(err, ErrReactivationNotDrained):
		return ActivationFailureClassNotDrained
	case errors.Is(err, ErrScheduleConflict):
		return ActivationFailureClassScheduleConflict
	case errors.Is(err, ErrActivationConflict):
		return ActivationFailureClassCASConflict
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ActivationFailureClassDependencyIO
	}
	var snapshotCorrupt *PersistedSnapshotCorruptError
	if errors.As(err, &snapshotCorrupt) {
		return ActivationFailureClassCorrupt
	}
	var scheduleCorrupt *DeterministicScheduleError
	if errors.As(err, &scheduleCorrupt) {
		return ActivationFailureClassCorrupt
	}
	return fallback
}
