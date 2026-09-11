package controlplane

import (
	"context"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
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
// Counts are populated only for reactivation; identities are bounded diagnostic
// samples, and neither identities nor raw errors become metric labels.
type ActivationFailure struct {
	Stage                                ActivationFailureStage
	Class                                ActivationFailureClass
	DrainingQueryGroups                  int
	CandidateQueryGroups                 int
	ReappearedQueryGroups                int
	ReappearedQueryGroupSamples          []execution.QueryGroupIdentity
	ReappearedQueryGroupSamplesTruncated bool
}

type ActivationFailureError struct {
	Failure ActivationFailure
	Err     error
}

// ActivationDependencyIOError marks an activation dependency call that did
// not complete. Unwrap preserves the original Redis, Progress or context error.
type ActivationDependencyIOError struct{ Err error }

// Error names the dependency that failed, not just that one did.
//
// It used to return a constant. Unwrap kept the original error, which serves
// errors.Is and errors.As, and loses it for everything that renders the error
// as text -- which is every log line. Three unrelated paths land in this class:
// the cutover CAS Eval failing, the Progress read that decides whether a Query
// Group has drained, and the schedule timeline read. On the wire they were the
// same sentence, so a deployment producing this failure hundreds of times gave
// no way to tell one cause from three, or a real dependency wobble from a
// condition that cannot resolve.
//
// Worse than missing: the constant made the failures look identical, and
// identical reads as "the same deterministic thing" to anyone counting distinct
// messages. The field meant to discriminate could not, by construction.
func (failure *ActivationDependencyIOError) Error() string {
	if failure == nil || failure.Err == nil {
		return "alarmd controlplane: activation dependency I/O failed"
	}
	return "alarmd controlplane: activation dependency I/O failed: " + failure.Err.Error()
}

func (failure *ActivationDependencyIOError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}

func activationDependencyIO(err error) error {
	if err == nil {
		return nil
	}
	var typed *ActivationDependencyIOError
	if errors.As(err, &typed) {
		return err
	}
	return &ActivationDependencyIOError{Err: err}
}

type PersistedActivationCorruptError struct{ Err error }

func (failure *PersistedActivationCorruptError) Error() string {
	return "alarmd controlplane: persisted activation is corrupt"
}

func (failure *PersistedActivationCorruptError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
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
		failure.ReappearedQueryGroups = counts[0].ReappearedQueryGroups
		if failure.Class == ActivationFailureClassNotDrained {
			failure.ReappearedQueryGroupSamples = append(
				[]execution.QueryGroupIdentity(nil), counts[0].ReappearedQueryGroupSamples...,
			)
			failure.ReappearedQueryGroupSamplesTruncated = counts[0].ReappearedQueryGroupSamplesTruncated
		}
	}
	return &ActivationFailureError{Failure: failure, Err: err}
}

func classifyActivationFailure(err error, fallback ActivationFailureClass) ActivationFailureClass {
	var dependencyIO *ActivationDependencyIOError
	if errors.As(err, &dependencyIO) {
		return ActivationFailureClassDependencyIO
	}
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
	var deterministicControlFact interface{ DeterministicControlFact() }
	if errors.As(err, &deterministicControlFact) {
		return ActivationFailureClassCorrupt
	}
	var snapshotCorrupt *PersistedSnapshotCorruptError
	var activationCorrupt *PersistedActivationCorruptError
	if errors.As(err, &snapshotCorrupt) || errors.As(err, &activationCorrupt) {
		return ActivationFailureClassCorrupt
	}
	var scheduleCorrupt *DeterministicScheduleError
	if errors.As(err, &scheduleCorrupt) {
		return ActivationFailureClassCorrupt
	}
	var activeSetConflict *ActiveQueryGroupSetConflictError
	if errors.As(err, &activeSetConflict) {
		return ActivationFailureClassProjectionConflict
	}
	return fallback
}
