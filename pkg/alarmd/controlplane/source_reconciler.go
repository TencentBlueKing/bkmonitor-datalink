package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const sourceCandidateSchemaVersion = "alarmd-control-source-candidate-v1"

type SourceRefreshStatus string

const (
	SourceRefreshPendingConfirmation SourceRefreshStatus = "PENDING_CONFIRMATION"
	SourceRefreshPublished           SourceRefreshStatus = "PUBLISHED"
	SourceRefreshUnchanged           SourceRefreshStatus = "UNCHANGED"
)

type SourceRefreshResult struct {
	Status      SourceRefreshStatus
	Observation string
	Publication SnapshotPublicationRef
}

type persistedSourceCandidate struct {
	SchemaVersion    string `json:"schema_version"`
	ConfirmationKey  string `json:"confirmation_key"`
	ObservationID    string `json:"observation_id"`
	SnapshotRevision string `json:"snapshot_revision"`
}

// SourceReconciler confirms changed Legacy observations across independent
// refresh calls. Its only durable intermediate fact is the candidate digest;
// it does not own a leader, retry queue or activation state machine.
type SourceReconciler struct {
	repository *RedisCatalogRepository
	publisher  *SnapshotPublisher
}

func NewSourceReconciler(repository *RedisCatalogRepository) (*SourceReconciler, error) {
	publisher, err := NewSnapshotPublisher(repository)
	if err != nil {
		return nil, err
	}
	return &SourceReconciler{repository: repository, publisher: publisher}, nil
}

func (reconciler *SourceReconciler) Refresh(
	ctx context.Context,
	source StrategySource,
	planner PrimaryQueryCompiler,
) (SourceRefreshResult, error) {
	if reconciler == nil || reconciler.repository == nil || reconciler.publisher == nil || source == nil || planner == nil {
		return SourceRefreshResult{}, errors.New("alarmd controlplane: incomplete source refresh request")
	}
	cycle, err := observeCycle(ctx, source)
	if err != nil {
		return SourceRefreshResult{}, err
	}
	observationID, err := deriveObservationID(cycle.strategies)
	if err != nil {
		return SourceRefreshResult{}, err
	}

	current, audit, err := reconciler.loadCurrent(ctx)
	if err != nil {
		return SourceRefreshResult{}, err
	}
	catalog, err := BuildCatalog(ctx, BuildRequest{Strategies: cycle.strategies, Planner: planner, LastGood: current})
	if err != nil {
		return SourceRefreshResult{}, err
	}
	if catalog.ObservationID != observationID {
		return SourceRefreshResult{}, errors.New("alarmd controlplane: source observation changed while building Catalog")
	}
	confirmationKey, err := sourceCandidateConfirmationKey(catalog.ObservationID, catalog.SnapshotRevision, catalog.Dispositions)
	if err != nil {
		return SourceRefreshResult{}, err
	}
	if audit != nil {
		currentKey, err := sourceCandidateConfirmationKey(audit.ObservationID, audit.Publication.SnapshotRevision, audit.Dispositions)
		if err != nil {
			return SourceRefreshResult{}, err
		}
		if currentKey == confirmationKey {
			snapshot, _, err := reconciler.publisher.Publish(ctx, catalog)
			if err != nil {
				return SourceRefreshResult{}, err
			}
			if err := reconciler.clearPending(ctx); err != nil {
				return SourceRefreshResult{}, err
			}
			return SourceRefreshResult{Status: SourceRefreshUnchanged, Observation: catalog.ObservationID, Publication: snapshot.Publication}, nil
		}
	}

	pending, err := reconciler.loadPending(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return SourceRefreshResult{}, err
	}
	if err == nil && pending.ConfirmationKey == confirmationKey {
		snapshot, _, err := reconciler.publisher.Publish(ctx, catalog)
		if err != nil {
			return SourceRefreshResult{}, err
		}
		if err := reconciler.clearPending(ctx); err != nil {
			return SourceRefreshResult{}, err
		}
		return SourceRefreshResult{Status: SourceRefreshPublished, Observation: catalog.ObservationID, Publication: snapshot.Publication}, nil
	}
	if err := reconciler.savePending(ctx, persistedSourceCandidate{SchemaVersion: sourceCandidateSchemaVersion,
		ConfirmationKey: confirmationKey, ObservationID: catalog.ObservationID, SnapshotRevision: string(catalog.SnapshotRevision)}); err != nil {
		return SourceRefreshResult{}, err
	}
	return SourceRefreshResult{Status: SourceRefreshPendingConfirmation, Observation: catalog.ObservationID}, nil
}

func (reconciler *SourceReconciler) loadCurrent(ctx context.Context) (*PublishedSnapshot, *SourceAuditState, error) {
	publication, err := reconciler.repository.LoadLatestPublication(ctx)
	if errors.Is(err, ErrSnapshotUnavailable) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	snapshot, err := reconciler.repository.LoadSnapshot(ctx, publication.SnapshotRevision)
	if err != nil {
		return nil, nil, err
	}
	audit, err := reconciler.repository.LoadLatestAudit(ctx)
	if errors.Is(err, ErrSnapshotUnavailable) {
		return &snapshot, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return &snapshot, &audit, nil
}

func sourceCandidateConfirmationKey(observationID string, revision execution.SnapshotRevision, dispositions []ObjectDisposition) (string, error) {
	if observationID == "" || revision == "" {
		return "", errors.New("alarmd controlplane: incomplete source candidate")
	}
	return contract.DeriveCanonicalDigestV2("alarmd-source-candidate-v1", struct {
		ObservationID string              `json:"observation_id"`
		Revision      string              `json:"snapshot_revision"`
		Dispositions  []ObjectDisposition `json:"dispositions"`
	}{ObservationID: observationID, Revision: string(revision), Dispositions: dispositions})
}

func (reconciler *SourceReconciler) loadPending(ctx context.Context) (persistedSourceCandidate, error) {
	payload, err := reconciler.repository.client.Get(ctx, reconciler.repository.sourceCandidateKey()).Bytes()
	if err != nil {
		return persistedSourceCandidate{}, err
	}
	var candidate persistedSourceCandidate
	if err := json.Unmarshal(payload, &candidate); err != nil {
		return persistedSourceCandidate{}, fmt.Errorf("alarmd controlplane: decode persisted source candidate: %w", err)
	}
	if candidate.SchemaVersion != sourceCandidateSchemaVersion || candidate.ConfirmationKey == "" ||
		candidate.ObservationID == "" || candidate.SnapshotRevision == "" {
		return persistedSourceCandidate{}, errors.New("alarmd controlplane: invalid persisted source candidate")
	}
	return candidate, nil
}

func (reconciler *SourceReconciler) savePending(ctx context.Context, candidate persistedSourceCandidate) error {
	payload, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	return reconciler.repository.client.Set(ctx, reconciler.repository.sourceCandidateKey(), payload, reconciler.repository.ttl).Err()
}

func (reconciler *SourceReconciler) clearPending(ctx context.Context) error {
	return reconciler.repository.client.Del(ctx, reconciler.repository.sourceCandidateKey()).Err()
}

func (repository *RedisCatalogRepository) sourceCandidateKey() string {
	return repository.prefix + ":source_candidate"
}
