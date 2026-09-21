// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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

// SourceReadMode says whether a refresh round read the strategy documents
// from the source or reused the observation of an earlier round.
type SourceReadMode string

const (
	SourceReadFull    SourceReadMode = "full"
	SourceReadSkipped SourceReadMode = "skipped"
)

// SourceReadReason says why a round read in the mode it did. A full read
// names the condition that forced it; a skipped round has only one reason.
type SourceReadReason string

const (
	// SourceReadChanged: the change signal or the active set moved since the
	// documents were last read.
	SourceReadChanged SourceReadReason = "changed"
	// SourceReadPending: the previous round did not end UNCHANGED, because a
	// candidate awaits confirmation, a publication just happened, or the round
	// failed. Confirmation is two independent reads of the source, so a
	// remembered observation never confirms anything.
	SourceReadPending SourceReadReason = "pending"
	// SourceReadPeriodic: sourceFullReadInterval passed since the last read.
	SourceReadPeriodic SourceReadReason = "periodic"
	// SourceReadMissing: the source offered no change signal this round.
	SourceReadMissing SourceReadReason = "missing"
	// SourceReadElected: this reconciler remembers no earlier read; the first
	// round of a process, or of a leader term.
	SourceReadElected SourceReadReason = "elected"
	// SourceReadUnchanged is the one reason of a skipped round: the signal and
	// the active set are what they were when the documents were last read.
	SourceReadUnchanged SourceReadReason = "unchanged"
)

// sourceFullReadInterval bounds how stale the Catalog may get for a change
// the source's publisher makes without moving its change signal: content it
// derives from other tables and rewrites in place. Six minutes is the
// staleness accepted for those changes. The bound is this reconciler's own:
// whatever the publisher rewrites, the next periodic read sees, so the number
// does not follow how often the publisher runs and need not move with it.
const sourceFullReadInterval = 6 * time.Minute

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
	// ReadMode and ReadReason say whether the round read the strategy
	// documents from the source or reused the previous round's observation,
	// and why. StrategiesRead is how many documents it asked the source for.
	ReadMode       SourceReadMode
	ReadReason     SourceReadReason
	StrategiesRead int
	// ChangeSignalPresent says the source offered a change signal this round,
	// and ChangeSignalAgeSeconds how long ago its publisher moved it, by this
	// process's clock. A signal that stops moving while strategies keep being
	// saved is the failure the age makes visible: without it, a reconciler
	// that skips forever and a source that never changes look the same.
	ChangeSignalPresent    bool
	ChangeSignalAgeSeconds int64
	// RetainedStaleRevisions is how many last-good Plans this round's
	// Catalog did not retain because their persisted revision no longer
	// derives from their facts. See BuildCatalog.
	RetainedStaleRevisions int
	// Composition is what the Catalog this round built is made of: Query
	// Groups and Plans by the data sources they query, and source objects by
	// disposition. Set on every round that got as far as a complete Catalog,
	// under any status -- a round that publishes nothing because nothing
	// changed composed the same Catalog as the one before it, and a reader
	// asking which data sources are running needs an answer then too.
	// Withheld names the objects whose disposition changed this round, so a
	// reader can ask which strategy is held back rather than only how many.
	// Empty on a round where nothing changed, which is the steady state.
	Withheld WithheldReport
	// Suspended names the strategies whose no-data half changed state this
	// round. Same shape and same budget as Withheld, different question: these
	// are evaluated, and only their absence detection is off.
	Suspended   WithheldReport
	Composition CatalogComposition
}

// sourceRoundMemory is what this reconciler last read from the source, kept
// so that a round the source signals nothing new for can reuse it instead of
// reading every document again. It is process memory only: a new leader term
// starts without one and reads everything, and nothing about it is written
// anywhere a later process could read back and compare against.
type sourceRoundMemory struct {
	signal SourceChangeSignal
	cycle  observedCycle
	readAt time.Time
	// steady marks that the round which last used this observation ended
	// UNCHANGED. Only a steady observation is reused: confirmation takes two
	// independent reads, and a round that failed proves nothing for the next.
	steady bool
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
	validateCatalog CatalogAdmission
	outputProtocol  string
	// candidates carries the compiler's output from one round to the next,
	// so a round compiles only the documents that changed. It lives on the
	// reconciler because that is the object that survives between rounds; a
	// follower's reconciler holds an empty one, as it never refreshes.
	candidates *CandidateCache
	// now paces the periodic full read and measures the change signal's age.
	now    func() time.Time
	memory *sourceRoundMemory
	// lastGood is the content of the latest publication this process knows,
	// kept in memory from the catalog it published or assembled once from
	// the object catalog after a restart; the whole snapshot body is no
	// longer read for it.
	lastGood *PublishedSnapshot
	// namedWithheld is the withheld objects this process has already written
	// out, so a round reports only what changed since it last said something.
	//
	// Process memory, deliberately, and not the published audit. The audit
	// records what a leader published; it says nothing about what was written
	// where an operator can read it, and the two came apart on the very
	// release that added these lines -- the audit was already in Redis,
	// published by leaders that had no such lines to write, so the first
	// leader that could write them found nothing to report and said nothing
	// at all. An operator arriving after a failover would have counts and no
	// names, which is the gap these lines exist to close. Nil on a process
	// that has said nothing, which is what makes its first round name
	// everything without needing a flag to say so.
	namedWithheld []ObjectDisposition
	// namedSuspended is the same memory for suspended no-data halves.
	namedSuspended []ObjectDisposition
}

// ConfigureClock sets the clock the reconciler paces its periodic full reads
// and measures the change signal's age by. It is set at assembly, where the
// process clock lives, so that a test can move it.
func (reconciler *SourceReconciler) ConfigureClock(now func() time.Time) error {
	if reconciler == nil {
		return errors.New("alarmd controlplane: no source reconciler")
	}
	if now == nil {
		return errors.New("alarmd controlplane: source reconciler clock is required")
	}
	reconciler.now = now
	return nil
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

// CatalogAdmission is the deployment's say over a Catalog the compiler built.
//
// It returns the Catalog to publish, which is how a deployment withholds the
// Plans it cannot serve while publishing the rest. Returning an error refuses
// the whole round instead, and is for conditions that are genuinely about the
// Catalog rather than about any Plan in it.
//
// The distinction is the point. This hook used to be able only to refuse, and
// the one condition production gave it -- a Plan needing longer Snapshot
// retention than the deployment keeps -- is a property of one Plan. A single
// strategy asking for sixty hours stopped every other strategy in the
// deployment from being published at all, on every round, for as long as it
// existed; the fleet went stale, no cutover was attempted, and the account of
// why was one sentence naming a strategy nobody had changed.
type CatalogAdmission func(Catalog) (Catalog, error)

func NewSourceReconciler(
	repository *RedisCatalogRepository,
	compiler RuntimePlanCompiler,
	stateSemantics strategy.StateSemantics,
	validators ...CatalogAdmission,
) (*SourceReconciler, error) {
	if compiler == nil || !validStateSemantics(stateSemantics) || len(validators) > 1 ||
		(len(validators) == 1 && validators[0] == nil) {
		return nil, errors.New("alarmd controlplane: invalid source reconciler compiler")
	}
	publisher, err := NewSnapshotPublisher(repository)
	if err != nil {
		return nil, err
	}
	var validateCatalog CatalogAdmission
	if len(validators) == 1 {
		validateCatalog = validators[0]
	}
	return &SourceReconciler{repository: repository, publisher: publisher, compiler: compiler,
		stateSemantics: stateSemantics, validateCatalog: validateCatalog, candidates: NewCandidateCache(),
		now: time.Now}, nil
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
	// Every outcome of a round that built a Catalog reports how it was built
	// and how its source was read; both are filled at the end rather than
	// copied into each return. A round that fails leaves its observation
	// unsettled, so the next round reads the source again.
	cycle, read, err := reconciler.observe(ctx, source)
	retainedStaleRevisions := 0
	var composition CatalogComposition
	var withheld, suspended WithheldReport
	defer func() {
		if err != nil {
			reconciler.unsettle()
			return
		}
		result.RetainedStaleRevisions = retainedStaleRevisions
		result.Composition = composition
		result.Withheld = withheld
		result.Suspended = suspended
		result.CompiledStrategies, result.ReusedStrategies = reconciler.candidates.Stats()
		result.ReadMode, result.ReadReason, result.StrategiesRead = read.mode, read.reason, read.strategies
		result.ChangeSignalPresent, result.ChangeSignalAgeSeconds = read.signalPresent, read.signalAgeSeconds
		reconciler.memory.steady = result.Status == SourceRefreshUnchanged
	}()
	if err != nil {
		return SourceRefreshResult{}, err
	}
	observationID, err := deriveObservationID(cycle.strategies)
	if err != nil {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitObservationID, err)
	}
	current, audit, err := reconciler.loadCurrent(ctx)
	if err != nil {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitLastGood, err)
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
		return SourceRefreshResult{}, exitAt(SourceRefreshExitBuildCatalog, err)
	}
	retainedStaleRevisions = catalog.RetainedStaleRevisions
	catalog, err = retainRuntimeExecutableCatalog(ctx, catalog, current, reconciler.compiler, reconciler.stateSemantics)
	if err != nil {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitRetainExecutable, err)
	}
	if catalog.ObservationID != observationID {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitObservationChanged,
			errors.New("alarmd controlplane: source observation changed while building Catalog"))
	}
	if reconciler.validateCatalog != nil {
		admitted, err := reconciler.validateCatalog(catalog)
		if err != nil {
			return SourceRefreshResult{}, exitAt(SourceRefreshExitValidateCatalog, err)
		}
		catalog = admitted
		// The revision names the content, so content the deployment withheld
		// has to be named by a different one. Publishing the admitted Catalog
		// under the revision the built one derived is refused at the write --
		// correctly, because the two would disagree about what that revision
		// contains, and everything downstream reads content by revision.
		catalog.SnapshotRevision, err = deriveSnapshotRevision(catalog.QueryGroups)
		if err != nil {
			return SourceRefreshResult{}, exitAt(SourceRefreshExitValidateCatalog, err)
		}
	}
	catalog.ObservationID = observationID
	composition = ComposeCatalog(catalog)
	// Named against what this process has already named, not against the
	// stored audit: see namedWithheld. The counts in the composition and these
	// lines come from one pass over one list, so the page and the log cannot
	// disagree about how many.
	withheld = ChangedWithheld(composition.WithheldObjects, reconciler.namedWithheld)
	reconciler.namedWithheld = RememberNamed(reconciler.namedWithheld, composition.WithheldObjects, withheld.Lines)
	// The same discipline for the strategies whose no-data half is suspended.
	// They are not withheld -- they are running -- so they get their own list
	// and their own stage, and the same changed-only rule: a deployment with a
	// standing set of them would otherwise repeat the whole set every round
	// and bury the one that just joined it.
	suspended = ChangedWithheld(composition.SuspendedNoDataObjects, reconciler.namedSuspended)
	reconciler.namedSuspended = RememberNamed(
		reconciler.namedSuspended, composition.SuspendedNoDataObjects, suspended.Lines)
	// The active revision remains the execution authority even when latest points
	// at a stranded candidate. Restore its occurrence directly; requiring two
	// identical source observations here can leave the active Snapshot expired
	// forever when non-semantic observation details change between refreshes.
	activation, activationErr := reconciler.repository.LoadActivation(ctx)
	if activationErr == nil && activation.Current.SnapshotRevision == catalog.SnapshotRevision {
		return reconciler.publish(ctx, current, catalog, SourceRefreshUnchanged)
	}
	if activationErr != nil && !errors.Is(activationErr, ErrActivationUnavailable) {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitActivation, activationErr)
	}
	confirmationKey, err := sourceCandidateConfirmationKey(catalog.ObservationID, catalog.SnapshotRevision, catalog.Dispositions)
	if err != nil {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitConfirmation, err)
	}
	if audit != nil {
		currentKey, err := sourceCandidateConfirmationKey(audit.ObservationID, audit.Publication.SnapshotRevision, audit.Dispositions)
		if err != nil {
			return SourceRefreshResult{}, exitAt(SourceRefreshExitConfirmation, err)
		}
		if currentKey == confirmationKey {
			return reconciler.publish(ctx, current, catalog, SourceRefreshUnchanged)
		}
	}

	pending, err := reconciler.loadPending(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitCandidate, err)
	}
	if err == nil && pending.ConfirmationKey == confirmationKey {
		return reconciler.publish(ctx, current, catalog, SourceRefreshPublished)
	}
	if err := reconciler.savePending(ctx, persistedSourceCandidate{SchemaVersion: sourceCandidateSchemaVersion,
		ConfirmationKey: confirmationKey, ObservationID: catalog.ObservationID, SnapshotRevision: string(catalog.SnapshotRevision)}); err != nil {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitCandidate, err)
	}
	pendingResult := SourceRefreshResult{Status: SourceRefreshPendingConfirmation, Observation: catalog.ObservationID}
	if current != nil {
		pendingResult.Latest = current.Publication
	}
	return pendingResult, nil
}

type sourceRead struct {
	mode             SourceReadMode
	reason           SourceReadReason
	strategies       int
	signalPresent    bool
	signalAgeSeconds int64
}

// observe returns the round's observation: read from the source when
// something forces it, the previous round's otherwise. Every round reads the
// change signal and, when it may skip, the active set; neither costs more
// than one small read, and together they are what the skip is decided on.
func (reconciler *SourceReconciler) observe(ctx context.Context, source StrategySource) (observedCycle, sourceRead, error) {
	now := reconciler.now()
	var signal SourceChangeSignal
	if signalled, ok := source.(ChangeSignalSource); ok {
		read, err := signalled.ChangeSignal(ctx)
		if err != nil {
			return observedCycle{}, sourceRead{}, exitAt(SourceRefreshExitChangeSignal, err)
		}
		signal = read
	}
	read := sourceRead{signalPresent: signal.Present}
	if signal.Present {
		read.signalAgeSeconds = int64(now.Sub(signal.WrittenAt) / time.Second)
	}
	reason, err := reconciler.fullReadReason(ctx, source, signal, now)
	if err != nil {
		return observedCycle{}, sourceRead{}, err
	}
	if reason == "" {
		read.mode, read.reason = SourceReadSkipped, SourceReadUnchanged
		return reconciler.memory.cycle, read, nil
	}
	cycle, err := observeCycle(ctx, source)
	if err != nil {
		return observedCycle{}, sourceRead{}, err
	}
	reconciler.memory = &sourceRoundMemory{signal: signal, cycle: cycle, readAt: now}
	read.mode, read.reason, read.strategies = SourceReadFull, reason, len(cycle.strategies)
	return cycle, read, nil
}

// fullReadReason names the condition that makes this round read every
// document; empty means the previous round's observation still stands. The
// conditions are checked from the ones that need no read to the one that
// does, so a round that must read anyway does not read the active set twice.
func (reconciler *SourceReconciler) fullReadReason(
	ctx context.Context,
	source StrategySource,
	signal SourceChangeSignal,
	now time.Time,
) (SourceReadReason, error) {
	memory := reconciler.memory
	switch {
	case memory == nil:
		return SourceReadElected, nil
	case !signal.Present:
		return SourceReadMissing, nil
	case !memory.steady:
		return SourceReadPending, nil
	case !now.Before(memory.readAt.Add(sourceFullReadInterval)):
		return SourceReadPeriodic, nil
	case signal.Value != memory.signal.Value:
		return SourceReadChanged, nil
	}
	ids, err := readActiveSet(ctx, source)
	if err != nil {
		return "", err
	}
	if !equalStrings(ids, memory.cycle.ids) {
		return SourceReadChanged, nil
	}
	return "", nil
}

func (reconciler *SourceReconciler) unsettle() {
	if reconciler.memory != nil {
		reconciler.memory.steady = false
	}
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
			return SourceRefreshResult{}, exitAt(SourceRefreshExitPublish, loadErr)
		}
		reconciler.rememberLastGood(snapshot.Publication, catalog)
		if clearErr := reconciler.clearPending(ctx); clearErr != nil {
			return SourceRefreshResult{}, exitAt(SourceRefreshExitCandidate, clearErr)
		}
		return SourceRefreshResult{Status: status, Observation: catalog.ObservationID,
			Publication: snapshot.Publication}, nil
	}
	if activationErr != nil && !errors.Is(activationErr, ErrActivationUnavailable) {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitActivation, activationErr)
	}
	expected := SnapshotPublicationRef{}
	if current != nil {
		expected = current.Publication
	}
	snapshot, _, err := reconciler.publisher.PublishIfCurrent(ctx, expected, catalog)
	if err == nil {
		reconciler.rememberLastGood(snapshot.Publication, catalog)
	}
	if errors.Is(err, ErrPublicationConflict) {
		winner, loadErr := reconciler.repository.LoadLatestPublication(ctx)
		if loadErr != nil {
			return SourceRefreshResult{}, exitAt(SourceRefreshExitPublish, loadErr)
		}
		if clearErr := reconciler.clearPending(ctx); clearErr != nil {
			return SourceRefreshResult{}, exitAt(SourceRefreshExitCandidate, clearErr)
		}
		return SourceRefreshResult{Status: SourceRefreshPublicationConflict,
			Observation: catalog.ObservationID, Publication: winner}, nil
	}
	if err != nil {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitPublish, err)
	}
	if err := reconciler.clearPending(ctx); err != nil {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitCandidate, err)
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
	snapshot, err := reconciler.currentSnapshot(ctx, publication)
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

// rememberLastGood keeps the content of a publication this process just
// made, so the next round's last-good catalog costs no read at all.
func (reconciler *SourceReconciler) rememberLastGood(publication SnapshotPublicationRef, catalog Catalog) {
	reconciler.lastGood = &PublishedSnapshot{SchemaVersion: snapshotSchemaVersion, Publication: publication,
		QueryGroups: append([]QueryGroup(nil), catalog.QueryGroups...)}
}

// currentSnapshot is the content of the latest publication: from memory
// when this process published it, otherwise assembled once from the object
// catalog and kept. A publication whose manifest is gone reads as an
// unavailable snapshot, as the body did.
func (reconciler *SourceReconciler) currentSnapshot(ctx context.Context, publication SnapshotPublicationRef) (PublishedSnapshot, error) {
	if reconciler.lastGood != nil && reconciler.lastGood.Publication == publication {
		return *reconciler.lastGood, nil
	}
	published, err := reconciler.repository.loadPublishedGroups(ctx, publication)
	if err != nil {
		return PublishedSnapshot{}, err
	}
	snapshot, err := reconciler.repository.snapshotOf(ctx, published)
	if err != nil {
		return PublishedSnapshot{}, err
	}
	reconciler.lastGood = &snapshot
	return snapshot, nil
}
