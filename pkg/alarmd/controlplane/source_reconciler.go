package controlplane

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// SourceRefreshStatus is how a round that read its source ended: it
// published a Catalog that differs from the latest publication, it found
// the one it built already published, or another writer published first.
// Every status names a publication.
type SourceRefreshStatus string

const (
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

// SourceRefreshBuild says whether a refresh round built its Catalog or stood
// on the previous round's, unchanged (SourceReconciler.reusableFor).
type SourceRefreshBuild string

const (
	SourceRefreshRebuilt SourceRefreshBuild = "rebuilt"
	SourceRefreshReused  SourceRefreshBuild = "reused"
)

// SourceRefreshBuilds is every build word, for a reader that pre-creates one
// series per word.
var SourceRefreshBuilds = []SourceRefreshBuild{SourceRefreshRebuilt, SourceRefreshReused}

// SourceReadReason says why a round read in the mode it did. A full read
// names the condition that forced it; a skipped round has only one reason.
type SourceReadReason string

const (
	// SourceReadChanged: the change signal or the active set moved since the
	// documents were last read.
	SourceReadChanged SourceReadReason = "changed"
	// SourceReadPending: the previous round did not end UNCHANGED, because it
	// published or it failed. A round that published may have read the source
	// in the middle of a write - a list written before its documents - and the
	// read after it is what takes the rest of that write in.
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

// SourceFullReadInterval is that bound, for the callers whose own freshness
// bound has to follow it rather than repeat it as a second number.
const SourceFullReadInterval = sourceFullReadInterval

type SourceRefreshResult struct {
	Status      SourceRefreshStatus
	Observation string
	Publication SnapshotPublicationRef
	// Latest is the publication the repository held as latest when the round
	// began, zero on a deployment that had published nothing. A caller that
	// finds no activation beside a non-zero Latest is looking at a store that
	// lost the record, not at a deployment activating for the first time.
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
	// Build says whether the round built its Catalog or reused the previous
	// round's; see SourceReconciler.reusableFor. Empty on a round that failed
	// before either.
	Build SourceRefreshBuild
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
	// LastGoodIdentityChanged is how many last-good Plans this round's
	// Catalog did not retain because the source now states another identity
	// for the strategy (Catalog.LastGoodIdentityChanged).
	LastGoodIdentityChanged int
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
	// holdsLastGood is the publisher's statement as it applies to this
	// observation: made for the change signal read with it, and about the
	// exact active set the cycle read (holdsLastGoodFor). It decides whether
	// a strategy the set no longer lists serves the removal grace
	// (BuildRequest.WriterHoldsLastGood), and a round that reuses the
	// observation reuses it with the documents.
	holdsLastGood bool
	// steady marks that the round which last used this observation ended
	// UNCHANGED. Only a steady observation is reused: a round that published
	// may have read a write half done, and a round that failed proves nothing
	// for the next.
	steady bool
}

// reusableRound is a round that ended UNCHANGED, kept whole so that the next
// one can stand on it when nothing it was built from has moved.
type reusableRound struct {
	catalog                 Catalog
	composition             CatalogComposition
	current                 *PublishedSnapshot
	roundKey                string
	retainedStaleRevisions  int
	lastGoodIdentityChanged int
	// until is the first moment the Catalog would change with every input
	// the same: the earliest end of an absence grace (absenceGraceEnd). Zero
	// when no strategy is serving one.
	until time.Time
}

// reusableFor is the previous round, when this round would build exactly its
// Catalog again, and nil otherwise. A Catalog is a pure function of the inputs
// BuildCatalog lists; each is held still here by one of these, and the
// activation head, read by the caller, holds the last:
//
//   - the observation: the round did not read the source (SourceReadSkipped)
//     and so reuses the previous round's documents, which a skip allows only
//     after that round ended UNCHANGED;
//   - the round key (roundKey): the same four parts the candidate cache is
//     emptied by;
//   - the clock: not yet at the first absence grace the Catalog serves;
//   - LastGood and the previous dispositions: this process is the one writer,
//     a term that ended forgot the round (StepDown), and the caller requires
//     the activation still at this Catalog's revision.
//
// The periodic full read (sourceFullReadInterval) is not a skipped round, so
// it always rebuilds: an input missing from this list stays frozen for at
// most that long, as the candidate cache would already hold it.
func (reconciler *SourceReconciler) reusableFor(read sourceRead, roundKey string, now time.Time) *reusableRound {
	reuse := reconciler.reusable
	switch {
	case reuse == nil, read.mode != SourceReadSkipped, reuse.roundKey != roundKey:
		return nil
	case !reuse.until.IsZero() && !now.Before(reuse.until):
		return nil
	}
	return reuse
}

// SourceReconciler turns each refresh round's read of the Legacy source into
// a publication: one consistent read (the active set before and after the
// documents, equal) is built and published by the round that made it. It
// keeps no durable intermediate fact, and it does not own a leader, retry
// queue or activation state machine.
type SourceReconciler struct {
	repository      *RedisCatalogRepository
	publisher       *SnapshotPublisher
	compiler        RuntimePlanCompiler
	stateSemantics  strategy.StateSemantics
	validateCatalog CatalogAdmission
	outputProtocol  string
	targetSources   TargetSources
	// noDataPolicy is read once per round rather than held as a value, so the
	// deployment can decide whether the horizon is fixed for the process or
	// follows something that moves while it runs. The round key covers it
	// either way, which is what makes a changed horizon reach Plans whose own
	// document did not change.
	//
	// Nil means the zero policy, which is a horizon of zero: absence tracked
	// indefinitely, the behaviour every Plan had before the horizon existed.
	noDataPolicy func() NoDataPolicy
	// candidates carries the compiler's output from one round to the next,
	// so a round compiles only the documents that changed. It lives on the
	// reconciler because that is the object that survives between rounds; a
	// follower's reconciler holds an empty one, as it never refreshes.
	candidates *CandidateCache
	// now paces the periodic full read and measures the change signal's age.
	now    func() time.Time
	memory *sourceRoundMemory
	// reusable is the last round that ended UNCHANGED, whole; see
	// reusableFor. Nil after any round that did not, and after StepDown:
	// steppedDown says one happened since the last round, which drops it.
	reusable    *reusableRound
	steppedDown atomic.Bool
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
	// strategies is the last publication indexed by strategy id, for
	// LookupStrategy; replaced whole at the end of each round that published
	// one or found it already published.
	strategies strategyLookupState
	// departed remembers the strategies this catalog let go, with the
	// identity each had while it still existed. Nothing else in the process
	// can supply that identity once the strategy is gone; see
	// DepartedStrategy.
	departed *departedMemory
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

// ConfigureTargetSources says which sources the deployment renders for a
// target plan's dynamic references, once, at assembly, for the same reason
// the output protocol is set rather than passed.
func (reconciler *SourceReconciler) ConfigureTargetSources(sources TargetSources) error {
	if reconciler == nil {
		return errors.New("alarmd controlplane: no source reconciler")
	}
	reconciler.targetSources = sources
	return nil
}

// ConfigureNoDataPolicy says what the deployment's no-data settings are for
// every Plan that does not state its own.
//
// A function rather than a value because the horizon is the deployment's to
// change, and decision-018 section 5.1 is about a horizon that moves while the
// process runs: the candidate cache is keyed by the strategy document, so a
// changed default that is not in the round key reaches only the strategies
// whose own document happens to change next, and reads as applied while doing
// nothing. Reading it per round is what lets the round key see the change.
//
// Configuring nothing leaves the zero policy, whose horizon of zero means the
// deployment set none, so absence is tracked indefinitely - what every Plan
// did before the horizon existed. The deployment says that by not writing the
// setting rather than by writing a zero, which its own configuration refuses;
// the zero only ever stands for absence by the time it reaches here. So a
// deployment that says nothing is not opted in, which is the direction that
// cannot surprise anyone: a horizon stops no-data alerts after it, and one
// arrived at by default would silence a real outage.
func (reconciler *SourceReconciler) ConfigureNoDataPolicy(policy func() NoDataPolicy) error {
	if reconciler == nil {
		return errors.New("alarmd controlplane: no source reconciler")
	}
	reconciler.noDataPolicy = policy
	return nil
}

// effectiveNoDataPolicy is the policy this round builds under.
func (reconciler *SourceReconciler) effectiveNoDataPolicy() NoDataPolicy {
	if reconciler == nil || reconciler.noDataPolicy == nil {
		return NoDataPolicy{}
	}
	return reconciler.noDataPolicy()
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
		departed: newDepartedMemory(), now: time.Now}, nil
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
	if reconciler.steppedDown.Swap(false) {
		reconciler.reusable = nil
	}
	// Every outcome of a round that built a Catalog reports how it was built
	// and how its source was read; both are filled at the end rather than
	// copied into each return. A round that fails leaves its observation
	// unsettled, so the next round reads the source again.
	cycle, read, err := reconciler.observe(ctx, source)
	retainedStaleRevisions, lastGoodIdentityChanged := 0, 0
	var composition CatalogComposition
	var withheld, suspended WithheldReport
	build := SourceRefreshRebuilt
	// The round the next one may stand on: set where this one ends
	// UNCHANGED, and kept only if it does.
	var reuseNext *reusableRound
	// latest is the publication that was latest when the round began.
	var latest *PublishedSnapshot
	defer func() {
		if err != nil {
			reconciler.unsettle()
			reconciler.reusable = nil
			return
		}
		if latest != nil {
			result.Latest = latest.Publication
		}
		result.Build = build
		result.RetainedStaleRevisions = retainedStaleRevisions
		result.LastGoodIdentityChanged = lastGoodIdentityChanged
		result.Composition = composition
		result.Withheld = withheld
		result.Suspended = suspended
		if build == SourceRefreshReused {
			result.CompiledStrategies, result.ReusedStrategies = 0, len(cycle.strategies)
		} else {
			result.CompiledStrategies, result.ReusedStrategies = reconciler.candidates.Stats()
		}
		result.ReadMode, result.ReadReason, result.StrategiesRead = read.mode, read.reason, read.strategies
		result.ChangeSignalPresent, result.ChangeSignalAgeSeconds = read.signalPresent, read.signalAgeSeconds
		reconciler.memory.steady = result.Status == SourceRefreshUnchanged
		if result.Status == SourceRefreshUnchanged {
			reconciler.reusable = reuseNext
		} else {
			reconciler.reusable = nil
		}
	}()
	if err != nil {
		return SourceRefreshResult{}, err
	}
	policy := reconciler.effectiveNoDataPolicy()
	roundKey, err := catalogRoundKey(planner, reconciler.outputProtocol, reconciler.targetSources, policy)
	if err != nil {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitBuildCatalog, err)
	}
	// Nothing this round would build from has moved since the previous round
	// ended UNCHANGED: publish that round's Catalog again rather than build
	// the same one. The publication step is the one an UNCHANGED round always
	// takes - the activation's objects renewed, the audit written - so the
	// round does everything but rebuild. See reusableFor.
	if reuse := reconciler.reusableFor(read, roundKey, reconciler.now()); reuse != nil {
		activation, activationErr := reconciler.repository.LoadActivationHead(ctx)
		if activationErr == nil && activation.Current.SnapshotRevision == reuse.catalog.SnapshotRevision {
			build, reuseNext, latest = SourceRefreshReused, reuse, reuse.current
			// A reused round's identity count is zero: the refusal left the
			// last-good Plan out of the catalog it published, so the rounds
			// after it have nothing to refuse again. Carried for symmetry
			// with the stale count, not because the carry matters.
			retainedStaleRevisions, lastGoodIdentityChanged, composition = reuse.retainedStaleRevisions, reuse.lastGoodIdentityChanged, reuse.composition
			withheld = ChangedWithheld(composition.WithheldObjects, reconciler.namedWithheld)
			reconciler.namedWithheld = RememberNamed(reconciler.namedWithheld, composition.WithheldObjects, withheld.Lines)
			suspended = ChangedWithheld(composition.SuspendedNoDataObjects, reconciler.namedSuspended)
			reconciler.namedSuspended = RememberNamed(reconciler.namedSuspended, composition.SuspendedNoDataObjects, suspended.Lines)
			return reconciler.publish(ctx, reuse.current, reuse.catalog, SourceRefreshUnchanged)
		}
	}
	observationID, err := deriveObservationID(cycle.strategies)
	if err != nil {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitObservationID, err)
	}
	current, audit, err := reconciler.loadCurrent(ctx)
	if err != nil {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitLastGood, err)
	}
	latest = current
	var previousDispositions []ObjectDisposition
	if audit != nil {
		previousDispositions = audit.Dispositions
	}
	catalog, err := BuildCatalog(ctx, BuildRequest{
		Strategies: cycle.strategies, Planner: planner, LastGood: current, PreviousDispositions: previousDispositions,
		Now: reconciler.now(), WriterHoldsLastGood: reconciler.memory.holdsLastGood,
		OutputProtocol: reconciler.outputProtocol, TargetSources: reconciler.targetSources, Cache: reconciler.candidates,
		NoDataPolicy: policy,
	})
	if err != nil {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitBuildCatalog, err)
	}
	retainedStaleRevisions = catalog.RetainedStaleRevisions
	catalog, err = retainRuntimeExecutableCatalog(ctx, catalog, current, reconciler.compiler, reconciler.stateSemantics)
	if err != nil {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitRetainExecutable, err)
	}
	// Read after the runtime step: it refuses last-good Plans of another
	// identity too, and adds its own to the build's.
	lastGoodIdentityChanged = catalog.LastGoodIdentityChanged
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
	remember := func() *reusableRound {
		return &reusableRound{catalog: catalog, composition: composition, current: current, roundKey: roundKey,
			retainedStaleRevisions: retainedStaleRevisions, lastGoodIdentityChanged: lastGoodIdentityChanged,
			until: absenceGraceEnd(catalog.Dispositions)}
	}
	// The Catalog the fleet already runs: restore its publication and say
	// nothing changed.
	activation, activationErr := reconciler.repository.LoadActivationHead(ctx)
	if activationErr == nil && activation.Current.SnapshotRevision == catalog.SnapshotRevision {
		reuseNext = remember()
		return reconciler.publish(ctx, current, catalog, SourceRefreshUnchanged)
	}
	if activationErr != nil && !errors.Is(activationErr, ErrActivationUnavailable) {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitActivation, activationErr)
	}
	// The Catalog the latest publication already holds, under the same
	// observation and audit: the activation lags it, or the store lost the
	// activation. Publishing it again is idempotent and returns that
	// publication, which the caller then activates; the round is UNCHANGED
	// so the next one may stand on it.
	if audit != nil && sameAudit(*audit, catalog) {
		reuseNext = remember()
		return reconciler.publish(ctx, current, catalog, SourceRefreshUnchanged)
	}
	// Anything else is a change, and the round that read it publishes it.
	// The read was consistent: the active set was the same before and after
	// the documents (observeCycle). A writer that writes its list before its
	// documents can still be read between the two, and what that costs is
	// bounded per strategy: a strategy whose new document is not written yet
	// keeps its last good Plan (or, new, is withheld as SOURCE_INCOMPLETE) and
	// one whose document is still the old one runs the old version, for one
	// round - this round published, so the next reads the source again
	// (SourceReadPending). Nothing is removed by it: a strategy leaves only by
	// being absent from the list.
	return reconciler.publish(ctx, current, catalog, SourceRefreshPublished)
}

// sameAudit is whether the published audit already says what this Catalog
// says: the same observation, the same revision and the same dispositions.
func sameAudit(audit SourceAuditState, catalog Catalog) bool {
	return audit.ObservationID == catalog.ObservationID && audit.Publication.SnapshotRevision == catalog.SnapshotRevision &&
		slices.Equal(audit.Dispositions, catalog.Dispositions)
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
	reconciler.memory = &sourceRoundMemory{signal: signal, cycle: cycle, readAt: now,
		holdsLastGood: holdsLastGoodFor(signal, cycle)}
	read.mode, read.reason, read.strategies = SourceReadFull, reason, len(cycle.strategies)
	return cycle, read, nil
}

// holdsLastGoodFor is whether the publisher's statement, read with this
// round's change signal, is about the active set this round's cycle read:
// the digest it names against the digest of the bytes the cycle's own read
// of the set returned. The statement is read before the cycle, so the two can
// come from different publications; when they do and the set differs, the
// digests differ and the statement is not taken. When they do and the set is
// byte for byte the one the statement names, the statement is true of the
// list this observation holds, which is all it is used for. A source that
// cannot name its bytes, or a statement without a digest, never matches.
func holdsLastGoodFor(signal SourceChangeSignal, cycle observedCycle) bool {
	return signal.HoldsLastGoodFor != "" && signal.HoldsLastGoodFor == cycle.activeSet
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
	activation, activationErr := reconciler.repository.LoadActivationHead(ctx)
	if activationErr == nil && activation.Current.SnapshotRevision == catalog.SnapshotRevision {
		snapshot, _, loadErr := reconciler.publisher.restoreIfActivationCurrent(ctx, activation, catalog)
		if loadErr != nil {
			return SourceRefreshResult{}, exitAt(SourceRefreshExitPublish, loadErr)
		}
		reconciler.rememberLastGood(snapshot.Publication, catalog)
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
		return SourceRefreshResult{Status: SourceRefreshPublicationConflict,
			Observation: catalog.ObservationID, Publication: winner}, nil
	}
	if err != nil {
		return SourceRefreshResult{}, exitAt(SourceRefreshExitPublish, err)
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
		// immutable payload expired. A round that reads the same source can
		// then recreate identical content at the same occurrence without
		// guessing any LastGood Plan body.
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

// rememberLastGood keeps the content of a publication this process just
// made, so the next round's last-good catalog costs no read at all.
func (reconciler *SourceReconciler) rememberLastGood(publication SnapshotPublicationRef, catalog Catalog) {
	groups := append([]QueryGroup(nil), catalog.QueryGroups...)
	// Before the new publication replaces the old one, record what left. The
	// two publications are the only moment both the strategy's absence and
	// its identity are in hand at once.
	if reconciler.lastGood != nil {
		reconciler.departed.record(reconciler.lastGood.QueryGroups, groups, reconciler.now())
	}
	reconciler.lastGood = &PublishedSnapshot{SchemaVersion: snapshotSchemaVersion, Publication: publication, QueryGroups: groups}
	reconciler.strategies.replace(buildStrategyIndex(publication, groups, catalog.Dispositions).
		withGlobal(catalog.GlobalStrategies).withObservation(catalog.ObservationID))
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
	// Assembled from the objects, so the dispositions of the round that
	// published it are not known here, and a lookup index without them
	// would answer "the source never listed it" for every strategy that
	// round withheld. No index is published from here: this process answers
	// nothing until a round it completes builds one with the dispositions.
	return snapshot, nil
}
