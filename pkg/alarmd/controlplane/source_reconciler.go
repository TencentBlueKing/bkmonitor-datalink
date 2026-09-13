package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

const sourceCandidateSchemaVersion = "alarmd-control-source-candidate-v1"

type SourceRefreshStatus string

const (
	SourceRefreshPendingConfirmation SourceRefreshStatus = "PENDING_CONFIRMATION"
	SourceRefreshPublished           SourceRefreshStatus = "PUBLISHED"
	SourceRefreshUnchanged           SourceRefreshStatus = "UNCHANGED"
	SourceRefreshPublicationConflict SourceRefreshStatus = "PUBLICATION_CONFLICT"
)

type SourceRefreshResult struct {
	Status      SourceRefreshStatus
	Observation string
	Publication SnapshotPublicationRef
	// Latest is the publication the repository holds as latest when a round
	// publishes nothing, so that a caller can still tell whether the fleet
	// executes it. A confirmed publication whose activation never happened
	// (the process stopped in between) stays latest through any number of
	// pending rounds, and only the activation step can bring the fleet to it.
	Latest SnapshotPublicationRef
	// CompiledStrategies and ReusedStrategies say how the round's Catalog was
	// built: how many strategies went through the compiler and how many were
	// taken from an earlier round's compilation of the same document. They
	// add up to the strategies the round asked the compiler about.
	CompiledStrategies int
	ReusedStrategies   int
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
	repository      *RedisCatalogRepository
	publisher       *SnapshotPublisher
	compiler        RuntimePlanCompiler
	stateSemantics  strategy.StateSemantics
	validateCatalog func(Catalog) error
	outputProtocol  string
	// candidates carries the compiler's output from one round to the next,
	// so a round compiles only the documents that changed. It lives on the
	// reconciler because that is the object that survives between rounds; a
	// follower's reconciler holds an empty one, as it never refreshes.
	candidates *CandidateCache
}

// ConfigureOutputProtocol sets the deployment's wire format choice, once, at
// assembly. Empty leaves the pre-choice behaviour, where the frozen revision
// decides. It is set rather than passed because the reconciler is built before
// the configuration reaches this layer, and a Plan built with the wrong choice
// would publish the wrong bytes for as long as it is cached.
func (reconciler *SourceReconciler) ConfigureOutputProtocol(protocol string) error {
	if reconciler == nil {
		return errors.New("alarmd controlplane: no source reconciler")
	}
	switch protocol {
	case "", outputProtocolAuto, outputProtocolLegacy, outputProtocolNative:
		reconciler.outputProtocol = protocol
		return nil
	default:
		return errors.New("alarmd controlplane: unknown output protocol")
	}
}

func NewSourceReconciler(
	repository *RedisCatalogRepository,
	compiler RuntimePlanCompiler,
	stateSemantics strategy.StateSemantics,
	validators ...func(Catalog) error,
) (*SourceReconciler, error) {
	if compiler == nil || !validStateSemantics(stateSemantics) || len(validators) > 1 ||
		(len(validators) == 1 && validators[0] == nil) {
		return nil, errors.New("alarmd controlplane: invalid source reconciler compiler")
	}
	publisher, err := NewSnapshotPublisher(repository)
	if err != nil {
		return nil, err
	}
	var validateCatalog func(Catalog) error
	if len(validators) == 1 {
		validateCatalog = validators[0]
	}
	return &SourceReconciler{repository: repository, publisher: publisher, compiler: compiler,
		stateSemantics: stateSemantics, validateCatalog: validateCatalog, candidates: NewCandidateCache()}, nil
}

func (reconciler *SourceReconciler) Refresh(
	ctx context.Context,
	source StrategySource,
	planner PrimaryQueryCompiler,
) (result SourceRefreshResult, err error) {
	if reconciler == nil || reconciler.repository == nil || reconciler.publisher == nil || reconciler.compiler == nil ||
		source == nil || planner == nil {
		return SourceRefreshResult{}, errors.New("alarmd controlplane: incomplete source refresh request")
	}
	// Every outcome of a round that built a Catalog reports how it was built;
	// the counts are read at the end rather than copied into each return.
	defer func() {
		if err == nil {
			result.CompiledStrategies, result.ReusedStrategies = reconciler.candidates.Stats()
		}
	}()
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
	var previousDispositions []ObjectDisposition
	if audit != nil {
		previousDispositions = audit.Dispositions
	}
	catalog, err := BuildCatalog(ctx, BuildRequest{
		Strategies: cycle.strategies, Planner: planner, LastGood: current, PreviousDispositions: previousDispositions,
		OutputProtocol: reconciler.outputProtocol, Cache: reconciler.candidates,
	})
	if err != nil {
		return SourceRefreshResult{}, err
	}
	catalog, err = retainRuntimeExecutableCatalog(ctx, catalog, current, reconciler.compiler, reconciler.stateSemantics)
	if err != nil {
		return SourceRefreshResult{}, err
	}
	if catalog.ObservationID != observationID {
		return SourceRefreshResult{}, errors.New("alarmd controlplane: source observation changed while building Catalog")
	}
	if reconciler.validateCatalog != nil {
		if err := reconciler.validateCatalog(catalog); err != nil {
			return SourceRefreshResult{}, err
		}
	}
	catalog.ObservationID = observationID
	// The active revision remains the execution authority even when latest points
	// at a stranded candidate. Restore its occurrence directly; requiring two
	// identical source observations here can leave the active Snapshot expired
	// forever when non-semantic observation details change between refreshes.
	activation, activationErr := reconciler.repository.LoadActivation(ctx)
	if activationErr == nil && activation.Current.SnapshotRevision == catalog.SnapshotRevision {
		return reconciler.publish(ctx, current, catalog, SourceRefreshUnchanged)
	}
	if activationErr != nil && !errors.Is(activationErr, ErrActivationUnavailable) {
		return SourceRefreshResult{}, activationErr
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
			return reconciler.publish(ctx, current, catalog, SourceRefreshUnchanged)
		}
	}

	pending, err := reconciler.loadPending(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return SourceRefreshResult{}, err
	}
	if err == nil && pending.ConfirmationKey == confirmationKey {
		return reconciler.publish(ctx, current, catalog, SourceRefreshPublished)
	}
	if err := reconciler.savePending(ctx, persistedSourceCandidate{SchemaVersion: sourceCandidateSchemaVersion,
		ConfirmationKey: confirmationKey, ObservationID: catalog.ObservationID, SnapshotRevision: string(catalog.SnapshotRevision)}); err != nil {
		return SourceRefreshResult{}, err
	}
	pendingResult := SourceRefreshResult{Status: SourceRefreshPendingConfirmation, Observation: catalog.ObservationID}
	if current != nil {
		pendingResult.Latest = current.Publication
	}
	return pendingResult, nil
}

func (reconciler *SourceReconciler) publish(
	ctx context.Context,
	current *PublishedSnapshot,
	catalog Catalog,
	status SourceRefreshStatus,
) (SourceRefreshResult, error) {
	activation, activationErr := reconciler.repository.LoadActivation(ctx)
	if activationErr == nil && activation.Current.SnapshotRevision == catalog.SnapshotRevision {
		snapshot, _, loadErr := reconciler.publisher.restoreIfActivationCurrent(ctx, activation, catalog)
		if loadErr != nil {
			return SourceRefreshResult{}, loadErr
		}
		if clearErr := reconciler.clearPending(ctx); clearErr != nil {
			return SourceRefreshResult{}, clearErr
		}
		return SourceRefreshResult{Status: status, Observation: catalog.ObservationID,
			Publication: snapshot.Publication}, nil
	}
	if activationErr != nil && !errors.Is(activationErr, ErrActivationUnavailable) {
		return SourceRefreshResult{}, activationErr
	}
	expected := SnapshotPublicationRef{}
	if current != nil {
		expected = current.Publication
	}
	snapshot, _, err := reconciler.publisher.PublishIfCurrent(ctx, expected, catalog)
	if errors.Is(err, ErrPublicationConflict) {
		winner, loadErr := reconciler.repository.LoadLatestPublication(ctx)
		if loadErr != nil {
			return SourceRefreshResult{}, loadErr
		}
		if clearErr := reconciler.clearPending(ctx); clearErr != nil {
			return SourceRefreshResult{}, clearErr
		}
		return SourceRefreshResult{Status: SourceRefreshPublicationConflict,
			Observation: catalog.ObservationID, Publication: winner}, nil
	}
	if err != nil {
		return SourceRefreshResult{}, err
	}
	if err := reconciler.clearPending(ctx); err != nil {
		return SourceRefreshResult{}, err
	}
	return SourceRefreshResult{Status: status, Observation: catalog.ObservationID, Publication: snapshot.Publication}, nil
}

func (reconciler *SourceReconciler) loadCurrent(ctx context.Context) (*PublishedSnapshot, *SourceAuditState, error) {
	publication, err := reconciler.repository.LoadLatestPublication(ctx)
	if errors.Is(err, ErrSnapshotUnavailable) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	snapshot, err := reconciler.repository.LoadPublishedSnapshot(ctx, publication)
	if errors.Is(err, ErrSnapshotUnavailable) {
		// Keep the latest publication as the CAS expectation even when its
		// immutable payload expired. A confirmed source observation can then
		// recreate identical content at the same occurrence without guessing
		// any LastGood Plan body.
		return &PublishedSnapshot{Publication: publication}, nil, nil
	}
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
