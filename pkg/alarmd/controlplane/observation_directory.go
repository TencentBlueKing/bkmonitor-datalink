// Tencent is pleased to support the open source community by making
// BlueKing available. Copyright (C) 2017-2025 Tencent. Licensed under the MIT License.

package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// DirectoryLimits is an observation allowance, never an execution limit.
// The caller derives it from the container allocation. WireBytes bounds each
// refresh, including metadata; Entries bounds the retained identity projection.
type DirectoryLimits struct {
	WireBytes int           `json:"refresh_wire_bytes"`
	Commands  int           `json:"refresh_commands"`
	Entries   int           `json:"retained_entries"`
	Timeout   time.Duration `json:"read_timeout_ns"`
	FreshFor  time.Duration `json:"fresh_for_ns"`
}

// DirectoryEntryReservationBytes includes overlapping refresh generations,
// copied activation facts, identity indexes and an audit disposition allowance.
// Variable strings are charged separately; this is not a hard process RSS cap.
func DirectoryEntryReservationBytes() int {
	return 2*(int(unsafe.Sizeof(StrategyDirectoryRow{}))+int(unsafe.Sizeof(execution.PlanActivationFact{}))+int(unsafe.Sizeof(execution.PlanIdentity{}))+256) + 512
}

var ErrObservationBudget = errors.New("observation budget exhausted")

var ErrObservationAmbiguous = errors.New("observation identity is ambiguous")
var ErrObservationChanged = errors.New("observation revision changed")

// ResolveCurrent checks the whole bounded strategy index, not a returned page.
func (d *ObservationDirectory) ResolveCurrent(at time.Time, tenant, business, strategy, group string, expectedRevision ...string) (StrategyDirectoryRow, error) {
	s := d.state.Load()
	if len(expectedRevision) > 0 && expectedRevision[0] != s.Revision {
		return StrategyDirectoryRow{}, ErrObservationChanged
	}
	if (!s.Complete && (tenant == "" || business == "" || strategy == "" || group == "")) || at.Sub(s.ObservedAt) > d.limits.FreshFor {
		return StrategyDirectoryRow{}, ErrSnapshotUnavailable
	}
	var identity execution.PlanIdentity
	var selected StrategyDirectoryRow
	for _, index := range s.byStrategy[strategy] {
		row := s.Rows[index]
		if tenant != "" && row.Identity.TenantID != tenant || business != "" && row.Identity.BusinessID != business {
			continue
		}
		if identity != (execution.PlanIdentity{}) && identity != row.Identity {
			return StrategyDirectoryRow{}, ErrObservationAmbiguous
		}
		identity = row.Identity
		if group != "" && string(row.QueryGroup) != group || row.Role != string(execution.ActivationCurrent) {
			continue
		}
		if selected.QueryGroup != "" {
			return StrategyDirectoryRow{}, ErrObservationAmbiguous
		}
		selected = row
	}
	if selected.QueryGroup == "" {
		return StrategyDirectoryRow{}, ErrSnapshotUnavailable
	}
	if selected.Activation != nil {
		fact := *selected.Activation
		selected.Activation = &fact
	}
	return selected, nil
}

type StrategyDirectoryRow struct {
	QueryRevision    execution.QueryRevision       `json:"query_revision"`
	ScheduleRevision execution.ScheduleRevision    `json:"schedule_revision"`
	Identity         execution.PlanIdentity        `json:"identity"`
	QueryGroup       execution.QueryGroupIdentity  `json:"query_group"`
	ObjectDigest     execution.ObjectDigest        `json:"object_digest"`
	Publication      SnapshotPublicationRef        `json:"publication"`
	Role             string                        `json:"role"`
	Activation       *execution.PlanActivationFact `json:"activation,omitempty"`
	// OutputContext names the output context this Plan renders by, which is
	// where its frozen wire format lives: the execution object above is
	// deliberately without it. Empty when the publication's manifest was not
	// in hand at refresh and the runtime had not remembered the content
	// either, which the output read reports as unknown rather than fetching a
	// manifest to find out.
	OutputContext execution.OutputContextDigest `json:"output_context_digest,omitempty"`
}

type StrategyDirectorySnapshot struct {
	Limits             DirectoryLimits        `json:"resource_limits"`
	ObservedAt         time.Time              `json:"observed_at"`
	Revision           string                 `json:"revision"`
	Complete           bool                   `json:"complete"`
	Reason             string                 `json:"reason,omitempty"`
	Published          SnapshotPublicationRef `json:"published"`
	Current            SnapshotPublicationRef `json:"current"`
	ActivationRevision uint64                 `json:"activation_revision"`
	SourceObservation  string                 `json:"source_observation"`
	SourceComplete     bool                   `json:"source_complete"`
	SourceReason       string                 `json:"source_reason,omitempty"`
	SourceMatchedTotal int                    `json:"source_matched_total"`
	SourceTruncated    bool                   `json:"source_truncated"`
	// Source dispositions have no tenant identity in the persisted contract.
	// They remain unattributed, never joined to a similarly named tenant Plan.
	Unattributed []ObjectDisposition    `json:"source_unattributed,omitempty"`
	Rows         []StrategyDirectoryRow `json:"rows"`
	ReadBytes    int                    `json:"read_bytes"`
	ReadCommands int                    `json:"read_commands"`
	GroupsKnown  int                    `json:"groups_known"`
	GroupsTotal  int                    `json:"groups_total"`
	// ManifestsFromIndex is how many of this refresh's manifests came from the
	// process's own catalog index rather than the store: the count a reader
	// checks to know the directory is riding the runtime's cache and not
	// re-reading the manifest on every refresh.
	ManifestsFromIndex int `json:"manifests_from_index"`
	byStrategy         map[string][]int
}

// ObservationDirectory is a disposable read projection over the existing
// manifest/index. Refresh runs on maintenance, never on an HTTP request and
// never in the control reconciler. It neither authorizes execution nor writes
// the catalog. One refresh owns the incremental projection; readers see an
// immutable page snapshot without waiting for Redis.
type ObservationDirectory struct {
	repository        *RedisCatalogRepository
	limits            DirectoryLimits
	refresh           sync.Mutex
	state             atomic.Pointer[StrategyDirectorySnapshot]
	known             map[execution.ObjectDigest]catalogIndexEntry
	readClient        redis.Cmdable
	cursor            map[execution.SnapshotRevision]int
	publicationCursor SnapshotPublicationRef
}

func NewObservationDirectory(repository *RedisCatalogRepository, limits DirectoryLimits, reader ...redis.Cmdable) (*ObservationDirectory, error) {
	if repository == nil || limits.WireBytes <= 0 || limits.Commands <= 0 || limits.Entries <= 0 || limits.Timeout <= 0 || limits.FreshFor <= 0 {
		return nil, errors.New("observation directory requires finite resource allowances")
	}
	d := &ObservationDirectory{repository: repository, limits: limits, known: make(map[execution.ObjectDigest]catalogIndexEntry), cursor: make(map[execution.SnapshotRevision]int)}
	d.readClient = repository.client
	if len(reader) > 0 && reader[0] != nil {
		d.readClient = reader[0]
	}
	d.state.Store(&StrategyDirectorySnapshot{Reason: "NOT_OBSERVED", Limits: limits})
	return d, nil
}

type directoryRead struct {
	repository      *RedisCatalogRepository
	client          redis.Cmdable
	bytes, commands int
	limits          DirectoryLimits
}

func (r *directoryRead) read(ctx context.Context, key string) ([]byte, error) {
	remaining := r.limits.WireBytes - r.bytes
	if remaining <= 0 || r.commands >= r.limits.Commands {
		return nil, ErrObservationBudget
	}
	r.commands++
	// GETRANGE bounds the response before allocation/JSON decoding. A single
	// extra byte distinguishes an oversized value from an exactly fitting one.
	payload, err := r.client.GetRange(ctx, key, 0, int64(remaining)).Bytes()
	r.bytes += len(payload)
	if err != nil {
		return nil, err
	}
	if len(payload) > remaining {
		return nil, ErrObservationBudget
	}
	if len(payload) == 0 {
		return nil, ErrSnapshotUnavailable
	}
	return payload, nil
}

func (r *directoryRead) decode(ctx context.Context, key string, out any) error {
	b, err := r.read(ctx, key)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (d *ObservationDirectory) Refresh(ctx context.Context, at time.Time) {
	if !d.refresh.TryLock() {
		return
	}
	defer d.refresh.Unlock()
	ctx, cancel := context.WithTimeout(ctx, d.limits.Timeout)
	defer cancel()
	r := directoryRead{repository: d.repository, client: d.readClient, limits: d.limits}
	s := &StrategyDirectorySnapshot{ObservedAt: at, Limits: d.limits, Rows: []StrategyDirectoryRow{}, byStrategy: map[string][]int{}}
	defer func() { s.ReadBytes, s.ReadCommands = r.bytes, r.commands; d.state.Store(s) }()
	fail := func(err error) {
		s.Complete = false
		s.Reason = "DEPENDENCY_UNAVAILABLE"
		if errors.Is(err, ErrObservationBudget) {
			s.Reason = "RESOURCE_BUDGET"
		}
	}
	payload, err := r.read(ctx, d.repository.latestPublicationKey())
	if err != nil {
		fail(err)
		return
	}
	parts := strings.Split(string(payload), "\n")
	if len(parts) != 2 {
		s.Reason = "INVALID_PUBLICATION"
		return
	}
	epoch, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		s.Reason = "INVALID_PUBLICATION"
		return
	}
	s.Published = SnapshotPublicationRef{SnapshotRevision: execution.SnapshotRevision(parts[1]), PublicationEpoch: epoch}
	if s.Published.validate() != nil {
		s.Reason = "INVALID_PUBLICATION"
		return
	}
	// The activation through the repository's header-checked cache, not a
	// budgeted read of the body: the runtime already holds the parsed
	// activation for the version it executes, a call costs the small header
	// and length reads, and the body is fetched only when the version moved
	// -- which the runtime would fetch on its next round anyway. Read on the
	// directory's own budget it was the whole body every refresh, on every
	// replica, for a diagnostics projection.
	activation, err := d.repository.LoadActivation(ctx)
	if err != nil {
		fail(err)
		return
	}
	if validateActivationState(activation) != nil {
		s.Reason = "INVALID_ACTIVATION"
		return
	}
	s.Current, s.ActivationRevision = activation.Current, activation.RecordRevision
	s.Revision = fmt.Sprintf("%s:%d:%d", s.Published.SnapshotRevision, s.Published.PublicationEpoch, activation.RecordRevision)
	active := make(map[execution.PlanIdentity]PlanActivationRecord, len(activation.Plans))
	publications := []SnapshotPublicationRef{s.Published}
	seen := map[SnapshotPublicationRef]bool{s.Published: true}
	for _, a := range activation.Plans {
		active[a.Fact.Plan] = a
		if !seen[a.Publication] && a.Publication.validate() == nil {
			publications = append(publications, a.Publication)
			seen[a.Publication] = true
		}
	}
	// Fairness also applies across publications: an oversized latest object
	// must not indefinitely hide a smaller, still-current carried publication.
	for i, pub := range publications {
		if pub == d.publicationCursor {
			publications = append(append([]SnapshotPublicationRef{}, publications[i:]...), publications[:i]...)
			break
		}
	}
	if len(publications) > 1 {
		d.publicationCursor = publications[1]
	}
	cachedRevision, cached := d.repository.catalogIndex.snapshot()
	nextKnown := make(map[execution.ObjectDigest]catalogIndexEntry)
	nextCursor := make(map[execution.SnapshotRevision]int)
	retained := 0
	retainedBytes := 0
	s.Complete = true
	for _, pub := range publications {
		// The manifest of the publication this process executes is already in
		// its catalog index, entry for entry: the index is replaced whole from
		// the manifest when the content is loaded, so for that revision the
		// index is the manifest and reading it again from the store is the
		// same bytes over the wire every refresh. Only a publication this
		// process has not loaded -- a carried one, or a cold start -- is read.
		var manifest CatalogManifest
		if cachedRevision == pub.SnapshotRevision && len(cached) > 0 {
			manifest = manifestFromIndex(pub.SnapshotRevision, cached)
			s.ManifestsFromIndex++
		} else if err = r.decode(ctx, d.repository.catalogManifestKey(pub.SnapshotRevision), &manifest); err != nil {
			fail(err)
			break
		}
		if manifest.SchemaVersion != catalogManifestSchemaVersion || manifest.SnapshotRevision != pub.SnapshotRevision {
			s.Complete = false
			s.Reason = "INVALID_MANIFEST"
			break
		}
		// One order however the manifest arrived, so the cursor below means
		// the same position from one refresh to the next.
		sort.Slice(manifest.QueryGroups, func(i, j int) bool { return manifest.QueryGroups[i].QueryGroup < manifest.QueryGroups[j].QueryGroup })
		s.GroupsTotal += len(manifest.QueryGroups)
		// Which output context each Plan renders by. A manifest read from the
		// store names them; one rebuilt from the index does not, and the
		// runtime's remembered content of the same publication does -- for
		// free, the same way the index stood in for the manifest. A row whose
		// publication neither has stays without one, and the read that wants
		// it says so rather than fetching a manifest per request to find out.
		contexts := manifestContextRefs(manifest)
		if len(contexts) == 0 {
			contexts = d.repository.rememberedContextRefs(pub)
		}
		start := d.cursor[pub.SnapshotRevision]
		budgetFailed := false
		for step := range manifest.QueryGroups {
			index := (start + step) % len(manifest.QueryGroups)
			ref := manifest.QueryGroups[index]
			entry, ok := cached[ref.QueryGroup]
			if !ok || entry.Digest != ref.ObjectDigest {
				entry, ok = nextKnown[ref.ObjectDigest]
				if !ok {
					entry, ok = d.known[ref.ObjectDigest]
				}
			}
			if !ok {
				var obj QueryGroupObject
				if cachedObject, _, hit := d.repository.objectCache.lookup(d.repository.queryGroupObjectKey(ref.ObjectDigest)); hit {
					obj = cachedObject.(storedQueryGroupObject).object
					err = nil
				} else {
					obj, err = d.readGroup(ctx, &r, ref.ObjectDigest)
				}
				if err != nil {
					if errors.Is(err, ErrObservationBudget) && !budgetFailed {
						nextCursor[pub.SnapshotRevision] = (index + 1) % len(manifest.QueryGroups)
						budgetFailed = true
					}
					fail(err)
					continue
				}
				if obj.Identity != ref.QueryGroup {
					s.Complete = false
					s.Reason = "INVALID_OBJECT_IDENTITY"
					continue
				}
				if len(obj.Plans) > d.limits.Entries-retained {
					fail(ErrObservationBudget)
					continue
				}
				entry = catalogIndexEntry{Group: obj.Identity, Digest: ref.ObjectDigest, QueryRevision: obj.QueryPlan.QueryRevision, ScheduleRevision: obj.ScheduleRevision}
				for _, p := range obj.Plans {
					entry.Plans = append(entry.Plans, p.Identity)
				}
			}
			if entry.Group != ref.QueryGroup {
				s.Complete = false
				s.Reason = "INVALID_OBJECT_IDENTITY"
				continue
			}
			// Include variable identity bytes, not only object count. The
			// reservation covers row/index/map headers and the overlap of
			// old/new projections; unusually long identities spend more of it.
			_, alreadyRetained := nextKnown[ref.ObjectDigest]
			if !alreadyRetained && len(entry.Plans) > d.limits.Entries-retained {
				fail(ErrObservationBudget)
				continue
			}
			if !alreadyRetained {
				entryBytes := len(string(ref.QueryGroup)) + len(string(ref.ObjectDigest))
				for _, id := range entry.Plans {
					entryBytes += DirectoryEntryReservationBytes() + len(id.TenantID) + len(id.BusinessID) + len(id.StrategyID) + len(string(contexts[id]))
				}
				if retainedBytes+entryBytes > d.limits.Entries*DirectoryEntryReservationBytes() {
					fail(ErrObservationBudget)
					continue
				}
				retained += len(entry.Plans)
				retainedBytes += entryBytes
			}
			nextKnown[ref.ObjectDigest] = entry
			s.GroupsKnown++
			for _, id := range entry.Plans {
				row := StrategyDirectoryRow{Identity: id, QueryGroup: ref.QueryGroup, ObjectDigest: ref.ObjectDigest, Publication: pub, Role: "PUBLISHED",
					QueryRevision: entry.QueryRevision, ScheduleRevision: entry.ScheduleRevision, OutputContext: contexts[id]}
				if a, exists := active[id]; exists && a.Publication == pub {
					fact := a.Fact
					row.Activation = &fact
					row.Role = string(fact.Selection)
				} else if pub != s.Published {
					continue
				}
				if len(s.Rows) >= d.limits.Entries {
					fail(ErrObservationBudget)
					break
				}
				s.Rows = append(s.Rows, row)
			}
		}
	}
	// Optional audit spends only what the identity/activation projection left.
	// A large audit must never starve the current strategy directory. It has
	// no tenant identity, so its source dispositions remain unattributed.
	s.SourceReason = "SOURCE_AUDIT_UNAVAILABLE"
	id, auditErr := r.read(ctx, d.repository.latestAuditKey())
	if auditErr == nil {
		var audit SourceAuditState
		auditErr = r.decode(ctx, d.repository.auditKey(string(id)), &audit)
		if auditErr == nil && audit.SchemaVersion == snapshotSchemaVersion && audit.ObservationID == string(id) {
			s.SourceObservation, s.SourceComplete, s.SourceReason = audit.ObservationID, true, ""
			s.Unattributed = audit.Dispositions[:min(len(audit.Dispositions), d.limits.Entries)]
			if len(audit.Dispositions) > d.limits.Entries {
				s.SourceComplete = false
				s.SourceReason = "RESOURCE_BUDGET"
			}
		}
	}
	if errors.Is(auditErr, ErrObservationBudget) {
		s.SourceReason = "RESOURCE_BUDGET"
	}
	d.known = nextKnown
	d.cursor = nextCursor
	sort.Slice(s.Rows, func(i, j int) bool {
		a, b := s.Rows[i], s.Rows[j]
		if a.Identity != b.Identity {
			return lessPlanIdentity(a.Identity, b.Identity)
		}
		if a.QueryGroup != b.QueryGroup {
			return a.QueryGroup < b.QueryGroup
		}
		return a.Publication.PublicationEpoch < b.Publication.PublicationEpoch
	})
	for i, row := range s.Rows {
		s.byStrategy[row.Identity.StrategyID] = append(s.byStrategy[row.Identity.StrategyID], i)
	}
	if s.Complete {
		s.Reason = ""
	}
}

// manifestFromIndex rebuilds a publication's manifest from the process's
// catalog index for that revision: the index was replaced whole from the
// manifest, so its keys and digests are the manifest's entries.
func manifestFromIndex(revision execution.SnapshotRevision, index map[execution.QueryGroupIdentity]catalogIndexEntry) CatalogManifest {
	manifest := CatalogManifest{SchemaVersion: catalogManifestSchemaVersion, SnapshotRevision: revision,
		QueryGroups: make([]ManifestQueryGroup, 0, len(index))}
	for group, entry := range index {
		manifest.QueryGroups = append(manifest.QueryGroups, ManifestQueryGroup{QueryGroup: group, ObjectDigest: entry.Digest})
	}
	return manifest
}

// manifestContextRefs is the manifest's Plan -> output context naming as a
// map; empty for a manifest rebuilt from the index, which carries none.
func manifestContextRefs(manifest CatalogManifest) map[execution.PlanIdentity]execution.OutputContextDigest {
	if len(manifest.Plans) == 0 {
		return nil
	}
	contexts := make(map[execution.PlanIdentity]execution.OutputContextDigest, len(manifest.Plans))
	for _, plan := range manifest.Plans {
		contexts[plan.Plan] = plan.ContextDigest
	}
	return contexts
}

// rememberedContextRefs is the same naming out of the content this process
// read last, when that content is the publication asked for. No read: the
// memo is what an activation round already paid for.
func (repository *RedisCatalogRepository) rememberedContextRefs(publication SnapshotPublicationRef) map[execution.PlanIdentity]execution.OutputContextDigest {
	if repository == nil {
		return nil
	}
	content, ok := repository.contentMemo.lookup(publication)
	if !ok {
		return nil
	}
	contexts := make(map[execution.PlanIdentity]execution.OutputContextDigest)
	for _, group := range content.Groups {
		for _, ref := range group.Refs {
			contexts[ref.Plan] = ref.Digest
		}
	}
	return contexts
}

// Why an output read could not say what a Plan publishes as. Closed: a
// reader shows these words and no others.
const (
	// OutputContextRefNotRetained: the directory row carries no output
	// context digest -- see StrategyDirectoryRow.OutputContext.
	OutputContextRefNotRetained = "OUTPUT_CONTEXT_REF_NOT_RETAINED"
	// OutputContextUnavailable: the object the row names is not in the store.
	OutputContextUnavailable = "OUTPUT_CONTEXT_UNAVAILABLE"
	// OutputContextCorrupt: bytes were there and did not hash to the digest
	// that names them, or did not decode as an output context.
	OutputContextCorrupt = "OUTPUT_CONTEXT_CORRUPT"
	// OutputContextBudget: the observation allowance was spent before the
	// read; the object is not known to be missing.
	OutputContextBudget = "RESOURCE_BUDGET"
	// OutputContextDependency: the store did not answer.
	OutputContextDependency = "DEPENDENCY_UNAVAILABLE"
)

// OutputFormatReasons is every reason OutputFormatFacts can carry.
var OutputFormatReasons = []string{OutputContextRefNotRetained, OutputContextUnavailable, OutputContextCorrupt, OutputContextBudget, OutputContextDependency}

// OutputFormatFacts is what one Plan's events are published as, read off the
// output context the control leader froze with the Plan -- not off the
// deployment's current choice, which is a different fact. It answers the
// question a forced choice leaves open and an automatic one leaves silent:
// for this strategy, which format did the choice come out as.
type OutputFormatFacts struct {
	// Known is whether the object was read. False comes with a Reason and
	// nothing else; the fields below are then not zero values of a fact but
	// the absence of one.
	Known  bool   `json:"known"`
	Reason string `json:"reason,omitempty"`
	// OutputContextDigest names the object read, so a reader can tell two
	// answers about one strategy apart when the Plan was rebuilt between them.
	OutputContextDigest execution.OutputContextDigest `json:"output_context_digest,omitempty"`
	// WireFormat is the word frozen in the object. Empty on an object written
	// before the choice existed, where the revision was the whole rule.
	WireFormat string `json:"wire_format,omitempty"`
	// EffectiveWireFormat is the format the sink writes for this Plan, and
	// DecidedBy how that is known, one of WireFormatDecisions: FROZEN when
	// the word above is there and is what is written, REVISION_RULE when the
	// rule the readers apply to an object without one decided it, and
	// HISTORICAL_WORD_RESOLVED when the word above is from an earlier rule
	// and the readers resolve it to a current format -- the two fields then
	// differ on purpose, and both are the truth.
	EffectiveWireFormat string `json:"effective_wire_format,omitempty"`
	DecidedBy           string `json:"decided_by,omitempty"`
	// SnapshotRevision is the frozen strategy revision the rule reads. Zero
	// is the input that sends a strategy the compatible way under auto.
	SnapshotRevision int64 `json:"snapshot_revision"`
	// SignalType is what the events are observed from, frozen beside the
	// format; empty when the build could not name it.
	SignalType string `json:"signal_type,omitempty"`
	// CompatibilityContext is whether the object carries the context the
	// Python-compatible conversion reads. It is attached when the Plan
	// publishes that protocol, so under a forced legacy choice a strategy with
	// a revision has it and under native none does.
	CompatibilityContext bool `json:"compatibility_context"`
}

// EffectiveOutput reads the output context a directory row names and says
// what the Plan publishes as. One immutable object, on the observation
// allowance, hashed against the digest that names it; the process's own
// object cache answers first, which on the replica that renders this Plan is
// every time. A row with no digest is answered as not retained, never by a
// manifest read: that is the read the directory exists to not make per
// request.
func (d *ObservationDirectory) EffectiveOutput(ctx context.Context, row StrategyDirectoryRow) OutputFormatFacts {
	if row.OutputContext == "" {
		return OutputFormatFacts{Reason: OutputContextRefNotRetained}
	}
	key := d.repository.outputContextKey(row.OutputContext)
	var object OutputContextObject
	if cached, _, hit := d.repository.objectCache.lookup(key); hit {
		object, hit = cached.(OutputContextObject)
		if !hit {
			return OutputFormatFacts{Reason: OutputContextCorrupt, OutputContextDigest: row.OutputContext}
		}
	} else {
		ctx, cancel := context.WithTimeout(ctx, d.limits.Timeout)
		defer cancel()
		r := directoryRead{repository: d.repository, client: d.readClient, limits: d.limits}
		payload, err := r.read(ctx, key)
		switch {
		case errors.Is(err, ErrObservationBudget):
			return OutputFormatFacts{Reason: OutputContextBudget, OutputContextDigest: row.OutputContext}
		case errors.Is(err, ErrSnapshotUnavailable):
			return OutputFormatFacts{Reason: OutputContextUnavailable, OutputContextDigest: row.OutputContext}
		case err != nil:
			return OutputFormatFacts{Reason: OutputContextDependency, OutputContextDigest: row.OutputContext}
		}
		hash, err := contract.DeriveCanonicalDigestV2OverCanonical(outputContextContractVersion, payload)
		if err != nil || hash != string(row.OutputContext) {
			return OutputFormatFacts{Reason: OutputContextCorrupt, OutputContextDigest: row.OutputContext}
		}
		if err = json.Unmarshal(payload, &object); err != nil || object.ContractVersion != outputContextContractVersion {
			return OutputFormatFacts{Reason: OutputContextCorrupt, OutputContextDigest: row.OutputContext}
		}
	}
	if object.Identity != row.Identity {
		return OutputFormatFacts{Reason: OutputContextCorrupt, OutputContextDigest: row.OutputContext}
	}
	format, decidedBy := EffectiveWireFormat(object.WireFormat, object.StrategyRef.SnapshotRevision)
	return OutputFormatFacts{
		Known: true, OutputContextDigest: row.OutputContext,
		WireFormat: object.WireFormat, EffectiveWireFormat: format, DecidedBy: decidedBy,
		SnapshotRevision: object.StrategyRef.SnapshotRevision, SignalType: object.SignalType,
		CompatibilityContext: object.LegacyOutput != nil,
	}
}

func (d *ObservationDirectory) readGroup(ctx context.Context, r *directoryRead, digest execution.ObjectDigest) (QueryGroupObject, error) {
	payload, err := r.read(ctx, d.repository.queryGroupObjectKey(digest))
	if err != nil {
		return QueryGroupObject{}, err
	}
	hash, err := contract.DeriveCanonicalDigestV2OverCanonical(queryGroupObjectContractVersion, payload)
	if err != nil || hash != string(digest) {
		return QueryGroupObject{}, ErrCatalogObjectCorrupt
	}
	var obj QueryGroupObject
	if err = json.Unmarshal(payload, &obj); err != nil || obj.ContractVersion != queryGroupObjectContractVersion {
		return QueryGroupObject{}, ErrCatalogObjectCorrupt
	}
	return obj, nil
}

// Page returns copied rows, never the mutable backing of a stored projection.
// No Redis call is made even during cold start or a dependency outage.
func (d *ObservationDirectory) Page(at time.Time, tenant, business, strategy string, offset, limit int) StrategyDirectorySnapshot {
	s := d.state.Load()
	out := *s
	out.byStrategy = nil
	out.Rows = []StrategyDirectoryRow{}
	out.Unattributed = nil
	for _, v := range s.Unattributed {
		if strategy != "" && v.SourceID == strategy {
			out.SourceMatchedTotal++
			if len(out.Unattributed) < limit {
				out.Unattributed = append(out.Unattributed, v)
			}
		}
	}
	out.SourceTruncated = out.SourceMatchedTotal > len(out.Unattributed) || !out.SourceComplete
	if at.Sub(s.ObservedAt) > d.limits.FreshFor {
		out.Complete = false
		out.SourceComplete = false
		out.SourceReason = "STALE"
		out.Reason = "STALE"
	}
	appendRow := func(row StrategyDirectoryRow) {
		if tenant != "" && row.Identity.TenantID != tenant || business != "" && row.Identity.BusinessID != business {
			return
		}
		if offset > 0 {
			offset--
			return
		}
		if len(out.Rows) < limit {
			if row.Activation != nil {
				fact := *row.Activation
				row.Activation = &fact
			}
			out.Rows = append(out.Rows, row)
		}
	}
	if strategy != "" {
		for _, i := range s.byStrategy[strategy] {
			appendRow(s.Rows[i])
		}
	} else {
		for _, row := range s.Rows {
			appendRow(row)
		}
	}
	return out
}

// EffectivePlan only accepts an identity/digest already in this observation's
// projection. It reads one immutable group; no sibling output contexts, source
// refresh or snapshot-body fallback can be triggered by an HTTP request.
func (d *ObservationDirectory) EffectivePlan(ctx context.Context, row StrategyDirectoryRow) (QueryGroupPlanObject, error) {
	ctx, cancel := context.WithTimeout(ctx, d.limits.Timeout)
	defer cancel()
	r := directoryRead{repository: d.repository, client: d.readClient, limits: d.limits}
	obj, err := d.readGroup(ctx, &r, row.ObjectDigest)
	if err != nil {
		return QueryGroupPlanObject{}, err
	}
	if obj.Identity != row.QueryGroup {
		return QueryGroupPlanObject{}, ErrCatalogObjectCorrupt
	}
	for _, plan := range obj.Plans {
		if plan.Identity == row.Identity {
			// Older objects omit the generation and let activation compilation
			// derive it. Never invent that field in the stored configuration.
			if row.Activation == nil || plan.StateGeneration != "" && plan.StateGeneration != row.Activation.Selected.StateGeneration || plan.ScheduleRevision != row.Activation.Selected.ScheduleRevision {
				return QueryGroupPlanObject{}, ErrCatalogObjectCorrupt
			}
			return plan, nil
		}
	}
	return QueryGroupPlanObject{}, ErrCatalogObjectUnavailable
}
