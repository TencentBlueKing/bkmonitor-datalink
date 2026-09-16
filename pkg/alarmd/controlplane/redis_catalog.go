package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const (
	snapshotSchemaVersion         = "alarmd-control-snapshot-v1"
	activationSchemaVersion       = "alarmd-control-activation-v2"
	legacyActivationSchemaVersion = "alarmd-control-activation-v1"
)

var (
	ErrSnapshotUnavailable            = errors.New("alarmd controlplane: snapshot unavailable")
	ErrCatalogObjectUnavailable       = errors.New("alarmd controlplane: catalog object unavailable")
	ErrActivationUnavailable          = errors.New("alarmd controlplane: activation unavailable")
	ErrActivationConflict             = errors.New("alarmd controlplane: activation conflict")
	ErrActivationEpochCollision       = errors.New("alarmd controlplane: Schedule activation publication epoch collision")
	ErrReactivationNotDrained         = errors.New("alarmd controlplane: Query Group cannot reactivate before retirement drains")
	ErrPublicationConflict            = errors.New("alarmd controlplane: publication conflict")
	ErrPublicationOccurrenceCollision = errors.New("alarmd controlplane: publication occurrence collision")
)

// PersistedSnapshotCorruptError identifies persisted bytes that were read
// successfully but cannot prove the immutable Snapshot fact they claim to
// contain. Transport and Redis command errors deliberately do not use this
// type: callers may retry those without treating the control fact as corrupt.
type PersistedSnapshotCorruptError struct {
	Err error
}

func (err *PersistedSnapshotCorruptError) Error() string {
	return fmt.Sprintf("alarmd controlplane: persisted snapshot is corrupt: %v", err.Err)
}

func (err *PersistedSnapshotCorruptError) Unwrap() error { return err.Err }

type SnapshotPublicationRef struct {
	SnapshotRevision execution.SnapshotRevision `json:"snapshot_revision"`
	PublicationEpoch uint64                     `json:"publication_epoch"`
}

func (reference SnapshotPublicationRef) validate() error {
	if reference.SnapshotRevision == "" || reference.PublicationEpoch == 0 {
		return errors.New("alarmd controlplane: incomplete snapshot publication reference")
	}
	return nil
}

// PublishedSnapshot is the immutable execution content. Observation health and
// dispositions are persisted separately and therefore cannot churn the
// snapshot revision or publication epoch.
type PublishedSnapshot struct {
	SchemaVersion string                 `json:"schema_version"`
	Publication   SnapshotPublicationRef `json:"publication"`
	QueryGroups   []QueryGroup           `json:"query_groups"`
}

type SourceAuditState struct {
	SchemaVersion string                 `json:"schema_version"`
	ObservationID string                 `json:"observation_id"`
	Publication   SnapshotPublicationRef `json:"publication"`
	Dispositions  []ObjectDisposition    `json:"dispositions"`
}

type PlanActivationRecord struct {
	Fact        execution.PlanActivationFact `json:"fact"`
	Publication SnapshotPublicationRef       `json:"publication"`
}

type DrainingQueryGroup struct {
	QueryGroup      execution.QueryGroupIdentity `json:"query_group"`
	RetiredBoundary execution.EvaluationTime     `json:"retired_boundary"`
}

// ActivationState is storage for the one activation fact owned by module 02.
// This package supplies persistence and reads only; SnapshotPublisher never
// mutates activation.
type ActivationState struct {
	SchemaVersion  string                  `json:"schema_version"`
	RecordRevision uint64                  `json:"record_revision"`
	Current        SnapshotPublicationRef  `json:"current"`
	Pending        *SnapshotPublicationRef `json:"pending,omitempty"`
	Plans          []PlanActivationRecord  `json:"plans"`
	Draining       []DrainingQueryGroup    `json:"draining,omitempty"`
	ActiveQGSetRef ActiveQueryGroupSetRef  `json:"active_qg_set_ref"`
}

type ActivationExpectation struct {
	RecordRevision uint64
	Current        SnapshotPublicationRef
	Pending        *SnapshotPublicationRef
}

type RedisCatalogRepository struct {
	client          redis.Cmdable
	prefix          string
	ttl             time.Duration
	activationCache parsedActivationCache
	objectCatalog   objectCatalogState
	catalogIndex    catalogIndex
	contentMemo     publishedContentMemo
	objectCache     *objectReadCache
	objectFlights   objectReadFlights
	// manifestCache and latestPublication bound the two reads the per-Slot
	// Segment freshness check makes. See segment_freshness_cache.go.
	manifestCache              catalogManifestCache
	manifestFlights            catalogManifestFlights
	latestPublication          latestPublicationMemo
	freshnessClock             func() time.Time
	controlCache               *controlReadCache
	controlReads               controlReadCounters
	adoptMu                    sync.Mutex
	legacyMigrationMaxScanKeys int
	legacyMigrationTimeout     time.Duration
	drainingRetireAfter        time.Duration
	segmentRetention           execution.SlotRetention
	// contentCutoverVerified is set once this process has read every open
	// Segment in one cutover and found each naming the content its manifest
	// names; later cutovers then read only the Query Groups whose content
	// changed. See content_cutover.go.
	contentCutoverVerified atomic.Bool
	observer               observability.Observer
}

// ConfigureDrainingTermination bounds how long a retired Query Group may stay
// in the persisted Draining projection without draining. The bound is derived
// from the scheduler replay age: once every Slot before the retirement
// boundary is older than the replay window nothing can execute it anymore, so
// the Draining fact carries no execution meaning and is pruned on the next
// publication activation. Zero disables pruning by age.
func (repository *RedisCatalogRepository) ConfigureDrainingTermination(maxReplayAge time.Duration) error {
	if repository == nil || maxReplayAge < 0 {
		return errors.New("alarmd controlplane: invalid draining termination replay age")
	}
	repository.drainingRetireAfter = DrainingTerminationWindow(maxReplayAge)
	return nil
}

func (repository *RedisCatalogRepository) ConfigureObserver(observer observability.Observer) {
	if repository != nil {
		repository.observer = observer
	}
}

func (repository *RedisCatalogRepository) observe(ctx context.Context, observation observability.Observation) {
	if repository != nil && repository.observer != nil {
		repository.observer.Observe(ctx, observation)
	}
}

func NewRedisCatalogRepository(client redis.Cmdable, prefix string, ttl time.Duration) (*RedisCatalogRepository, error) {
	if client == nil || prefix == "" || strings.ContainsAny(prefix, "{} \t\r\n") || ttl <= 0 {
		return nil, errors.New("alarmd controlplane: invalid Redis catalog repository")
	}
	return &RedisCatalogRepository{client: client, prefix: prefix, ttl: ttl,
		controlCache: newControlReadCache(
			controlTimelineCacheDefaultMaxEntries, controlTimelineCacheDefaultMaxBytes)}, nil
}

func (repository *RedisCatalogRepository) ConfigureLegacyMigration(maxScanKeys int, timeout time.Duration) error {
	if repository == nil || maxScanKeys <= 0 || timeout <= 0 {
		return errors.New("alarmd controlplane: invalid legacy migration bounds")
	}
	repository.legacyMigrationMaxScanKeys, repository.legacyMigrationTimeout = maxScanKeys, timeout
	return nil
}

func (repository *RedisCatalogRepository) Ping(ctx context.Context) error {
	if repository == nil || repository.client == nil {
		return errors.New("alarmd controlplane: Redis catalog repository is required")
	}
	return repository.client.Ping(ctx).Err()
}

// renewCurrentActivationObjectsScript moves the expiry of the objects one
// Activation header names, and of nothing else. The Snapshot and the Active
// Set are content-addressed, so their presence is all the script needs to
// know about them; the two small mappings are compared by value, and a header
// that moved means another Activation owns the objects now. The reply carries
// the Active Set's size for the renewal facts, so the caller never reads it
// to learn that.
const renewCurrentActivationObjectsScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return {0, 0} end
if redis.call('EXISTS', KEYS[2]) == 0 or redis.call('GET', KEYS[3]) ~= ARGV[3] or
   redis.call('GET', KEYS[4]) ~= ARGV[4] or redis.call('EXISTS', KEYS[5]) == 0 then return {-1, 0} end
redis.call('PEXPIRE', KEYS[2], ARGV[2])
redis.call('PEXPIRE', KEYS[3], ARGV[2])
redis.call('PEXPIRE', KEYS[4], ARGV[2])
redis.call('PEXPIRE', KEYS[5], ARGV[2])
return {1, redis.call('STRLEN', KEYS[5])}
`

// RenewCurrentActivationObjects renews only the complete Snapshot occurrence
// and Active Set named by the same Activation header, together with the
// Schedule timelines that Activation still references. A concurrent cutover
// cannot renew stale facts. It runs on every refresh round, so it moves no
// content: the Snapshot and Active Set are proved readable through the
// repository's own verified reads, which reuse what an earlier round loaded,
// and the script only checks that the keys are still there.
func (repository *RedisCatalogRepository) RenewCurrentActivationObjects(ctx context.Context) error {
	started := time.Now()
	metricResult := "failure"
	queryGroups, objectBytes := 0, 0
	defer func() {
		repository.observe(ctx, observability.Observation{Component: observability.ComponentControlPlane, Stage: observability.StageActiveQGSet,
			Result: observability.Result(metricResult), ActiveQGSet: &observability.ActiveQGSetFacts{Operation: "renew", Result: metricResult,
				QueryGroups: queryGroups, ObjectBytes: objectBytes, Duration: time.Since(started)}})
	}()
	state, err := repository.LoadActivation(ctx)
	if err != nil {
		return err
	}
	header, err := activationHeader(state.RecordRevision, state.Current, state.Pending)
	if err != nil {
		return err
	}
	if _, err := repository.LoadCatalogManifest(ctx, state.Current.SnapshotRevision); err != nil {
		if errors.Is(err, ErrCatalogManifestUnavailable) {
			return ErrSnapshotUnavailable
		}
		return err
	}
	groups, err := repository.LoadActiveQueryGroupSet(ctx, state.ActiveQGSetRef)
	if err != nil {
		return err
	}
	// The manifest is the current content that must still be there.
	result, err := repository.client.Eval(ctx, renewCurrentActivationObjectsScript, []string{
		repository.activationHeaderKey(),
		repository.catalogManifestKey(state.Current.SnapshotRevision),
		repository.epochForRevisionKey(state.Current.SnapshotRevision),
		repository.publicationKey(state.Current.PublicationEpoch),
		repository.activeQGSetKey(state.ActiveQGSetRef.Digest),
	}, header, repository.ttl.Milliseconds(), strconv.FormatUint(state.Current.PublicationEpoch, 10), string(state.Current.SnapshotRevision)).Slice()
	if err != nil {
		return fmt.Errorf("alarmd controlplane: renew current activation objects: %w", err)
	}
	if len(result) != 2 {
		return errors.New("alarmd controlplane: invalid activation renewal result")
	}
	outcome, err := redisInteger(result[0])
	if err != nil {
		return errors.New("alarmd controlplane: invalid activation renewal result")
	}
	activeBytes, _ := redisInteger(result[1])
	switch outcome {
	case 1:
		// The Schedule timelines of the renewed Active Set and of the Draining
		// projection are renewed only after the guarded CAS proved that the
		// state read above is still the current one.
		if err := repository.renewScheduleTimelines(ctx, groups, state.Draining); err != nil {
			return err
		}
		repository.renewObjectCatalog(ctx, state.Current.SnapshotRevision)
		metricResult = "success"
		queryGroups, objectBytes = len(groups), int(activeBytes)
		return nil
	case 0:
		return ErrActivationConflict
	default:
		return ErrSnapshotUnavailable
	}
}

// publishSnapshotScript records one publication occurrence of a revision
// whose content the object catalog already holds: the manifest and objects
// are written before this script runs, and a revision that would name
// different content under the same digest is refused there. KEYS: epoch
// counter, epoch of the revision, latest publication, occurrence prefix.
// ARGV: TTL, revision, expected latest publication.
const publishSnapshotScript = `
local function occurrence_key(epoch)
  return KEYS[4] .. tostring(epoch)
end
local latest = redis.call('GET', KEYS[3])
if ARGV[3] == '' then
  if latest then return {-2, -2} end
elseif not latest or latest ~= ARGV[3] then
  return {-2, -2}
end
local latest_epoch = nil
local latest_revision = nil
if latest then
  latest_epoch, latest_revision = string.match(latest, '^(%d+)\n(.+)$')
  if not latest_epoch or not latest_revision then return {-3, -3} end
  local latest_occurrence = redis.call('GET', occurrence_key(latest_epoch))
  if latest_occurrence and latest_occurrence ~= latest_revision then return {-4, -4} end
end
if latest then
  redis.call('SET', occurrence_key(latest_epoch), latest_revision, 'PX', ARGV[1], 'NX')
end
local previous_epoch = redis.call('GET', KEYS[2])
if previous_epoch then
  redis.call('SET', occurrence_key(previous_epoch), ARGV[2], 'PX', ARGV[1], 'NX')
end
if latest then
  if latest_revision == ARGV[2] then
    redis.call('PSETEX', occurrence_key(latest_epoch), ARGV[1], latest_revision)
    redis.call('PSETEX', KEYS[2], ARGV[1], latest_epoch)
    redis.call('PEXPIRE', KEYS[3], ARGV[1])
    return {tonumber(latest_epoch), 0}
  end
end
local epoch = redis.call('INCR', KEYS[1])
redis.call('PSETEX', occurrence_key(epoch), ARGV[1], ARGV[2])
redis.call('PSETEX', KEYS[2], ARGV[1], tostring(epoch))
redis.call('PSETEX', KEYS[3], ARGV[1], tostring(epoch) .. '\n' .. ARGV[2])
return {tonumber(epoch), 1}
`

// renewSnapshotPublicationScript extends the life of the Snapshot occurrence
// the persistent Activation still selects without carrying any content. The
// manifest is the content key of a revision: one that exists needs only its
// expiry moved, and the small keys beside it are rewritten exactly as the
// publication path writes them. 2 says the manifest is gone and the caller
// has to write the content back before the occurrence can be restored.
const renewSnapshotPublicationScript = `
local header = redis.call('GET', KEYS[1])
if not header or header ~= ARGV[1] then return 0 end
local latest = redis.call('GET', KEYS[2])
if ARGV[6] == '' then
  if latest then return 0 end
elseif not latest or latest ~= ARGV[6] then
  return 0
end
if redis.call('EXISTS', KEYS[3]) == 0 then return 2 end
local occurrence = redis.call('GET', KEYS[5])
if occurrence and occurrence ~= ARGV[5] then return -2 end
redis.call('PEXPIRE', KEYS[3], ARGV[2])
redis.call('PSETEX', KEYS[4], ARGV[2], ARGV[3])
redis.call('PSETEX', KEYS[5], ARGV[2], ARGV[5])
redis.call('PSETEX', KEYS[2], ARGV[2], ARGV[4])
return 1
`

// restoreSnapshotPublicationScript recreates the small keys of an
// occurrence whose manifest the caller has just written back; a manifest
// still missing is reported as 2 rather than silently pointed at.
const restoreSnapshotPublicationScript = `
local header = redis.call('GET', KEYS[1])
if not header or header ~= ARGV[1] then return 0 end
local latest = redis.call('GET', KEYS[2])
if ARGV[6] == '' then
  if latest then return 0 end
elseif not latest or latest ~= ARGV[6] then
  return 0
end
if redis.call('EXISTS', KEYS[3]) == 0 then return 2 end
local occurrence = redis.call('GET', KEYS[5])
if occurrence and occurrence ~= ARGV[5] then return -2 end
redis.call('PEXPIRE', KEYS[3], ARGV[2])
redis.call('PSETEX', KEYS[4], ARGV[2], ARGV[3])
redis.call('PSETEX', KEYS[5], ARGV[2], ARGV[5])
redis.call('PSETEX', KEYS[2], ARGV[2], ARGV[4])
return 1
`

// restoreCatalogPublicationIfActivationCurrent keeps the immutable Catalog
// facts of the Snapshot occurrence the persistent Activation still selects
// alive, and recreates them only once they expired. It does not create a new
// epoch or mutate Schedule/Activation provenance. Every round whose source did
// not change comes through here, so the common case moves no content: the
// manifest only has its expiry extended, and the objects and manifest are
// written back only when the manifest is gone.
func (repository *RedisCatalogRepository) restoreCatalogPublicationIfActivationCurrent(
	ctx context.Context,
	activation ActivationState,
	catalog Catalog,
) (PublishedSnapshot, error) {
	if repository == nil || repository.client == nil ||
		validateActivationState(activation) != nil || catalog.SnapshotRevision == "" ||
		activation.Current.SnapshotRevision != catalog.SnapshotRevision {
		return PublishedSnapshot{}, errors.New("alarmd controlplane: invalid expired Snapshot restoration")
	}
	header, err := activationHeader(activation.RecordRevision, activation.Current, activation.Pending)
	if err != nil {
		return PublishedSnapshot{}, err
	}
	epoch := strconv.FormatUint(activation.Current.PublicationEpoch, 10)
	latest, latestErr := repository.client.Get(ctx, repository.latestPublicationKey()).Result()
	if errors.Is(latestErr, redis.Nil) {
		latest = ""
	} else if latestErr != nil {
		return PublishedSnapshot{}, latestErr
	}
	if err := repository.ensureObjectCatalog(ctx, catalog); err != nil {
		return PublishedSnapshot{}, err
	}
	keys := []string{
		repository.activationHeaderKey(), repository.latestPublicationKey(),
		repository.catalogManifestKey(catalog.SnapshotRevision), repository.epochForRevisionKey(catalog.SnapshotRevision),
		repository.publicationKey(activation.Current.PublicationEpoch),
	}
	restored := PublishedSnapshot{SchemaVersion: snapshotSchemaVersion, Publication: activation.Current,
		QueryGroups: append([]QueryGroup(nil), catalog.QueryGroups...)}
	renewed, err := repository.client.Eval(ctx, renewSnapshotPublicationScript, keys,
		header, repository.ttl.Milliseconds(), epoch, publicationValue(activation.Current),
		string(catalog.SnapshotRevision), latest).Int()
	if err != nil {
		return PublishedSnapshot{}, fmt.Errorf("alarmd controlplane: renew Snapshot publication: %w", err)
	}
	switch renewed {
	case 1:
		return restored, nil
	case 2:
	case -2:
		return PublishedSnapshot{}, ErrPublicationOccurrenceCollision
	case 0:
		return PublishedSnapshot{}, ErrPublicationConflict
	default:
		return PublishedSnapshot{}, errors.New("alarmd controlplane: invalid Snapshot renewal result")
	}
	// The manifest expired while this process still remembered the revision
	// as written: forget that, write the objects and the manifest back, and
	// only then restore the occurrence's small keys. The revision is proven
	// to name the content before anything is written.
	revision, err := deriveSnapshotRevision(catalog.QueryGroups)
	if err != nil || revision != catalog.SnapshotRevision {
		return PublishedSnapshot{}, errors.New("alarmd controlplane: restored Snapshot revision does not match Catalog")
	}
	repository.objectCatalog.forget()
	if err := repository.ensureObjectCatalog(ctx, catalog); err != nil {
		return PublishedSnapshot{}, err
	}
	changed, err := repository.client.Eval(ctx, restoreSnapshotPublicationScript, keys,
		header, repository.ttl.Milliseconds(), epoch, publicationValue(activation.Current),
		string(catalog.SnapshotRevision), latest).Int()
	if err != nil {
		return PublishedSnapshot{}, fmt.Errorf("alarmd controlplane: restore expired Snapshot: %w", err)
	}
	if changed == 2 {
		return PublishedSnapshot{}, ErrSnapshotUnavailable
	}
	if changed == -2 {
		return PublishedSnapshot{}, ErrPublicationOccurrenceCollision
	}
	if changed != 1 {
		return PublishedSnapshot{}, ErrPublicationConflict
	}
	return restored, nil
}

func (repository *RedisCatalogRepository) PublishCatalog(ctx context.Context, catalog Catalog) (PublishedSnapshot, bool, error) {
	expected, err := repository.LoadLatestPublication(ctx)
	if errors.Is(err, ErrSnapshotUnavailable) {
		expected = SnapshotPublicationRef{}
	} else if err != nil {
		return PublishedSnapshot{}, false, err
	}
	return repository.PublishCatalogIfCurrent(ctx, expected, catalog)
}

// PublishCatalogIfCurrent persists one publication occurrence only while the
// caller's previously read latest publication remains current. Snapshot
// content stays addressed by revision; an historical revision returning after
// an intervening publication receives a new epoch.
func (repository *RedisCatalogRepository) PublishCatalogIfCurrent(
	ctx context.Context,
	expected SnapshotPublicationRef,
	catalog Catalog,
) (PublishedSnapshot, bool, error) {
	if repository == nil || repository.client == nil || catalog.SnapshotRevision == "" || catalog.QueryGroups == nil {
		return PublishedSnapshot{}, false, errors.New("alarmd controlplane: incomplete catalog publication")
	}
	if expected != (SnapshotPublicationRef{}) && expected.validate() != nil {
		return PublishedSnapshot{}, false, errors.New("alarmd controlplane: invalid expected publication")
	}
	revision, err := deriveSnapshotRevision(catalog.QueryGroups)
	if err != nil {
		return PublishedSnapshot{}, false, err
	}
	if revision != catalog.SnapshotRevision {
		return PublishedSnapshot{}, false, errors.New("alarmd controlplane: snapshot revision does not match catalog content")
	}
	expectedValue := ""
	if expected != (SnapshotPublicationRef{}) {
		expectedValue = publicationValue(expected)
	}
	// The objects and the manifest are written before the publication
	// decides: they are content-addressed, so a publication that then loses
	// its compare-and-set leaves nothing wrong behind, and a publication that
	// wins never names content that is not stored.
	if err := repository.ensureObjectCatalog(ctx, catalog); err != nil {
		return PublishedSnapshot{}, false, err
	}
	result, err := repository.client.Eval(ctx, publishSnapshotScript, []string{
		repository.epochCounterKey(), repository.epochForRevisionKey(catalog.SnapshotRevision),
		repository.latestPublicationKey(), repository.publicationKeyPrefix(),
	}, repository.ttl.Milliseconds(), string(catalog.SnapshotRevision), expectedValue).Slice()
	if err != nil {
		return PublishedSnapshot{}, false, fmt.Errorf("alarmd controlplane: publish snapshot: %w", err)
	}
	if len(result) != 2 {
		return PublishedSnapshot{}, false, errors.New("alarmd controlplane: invalid snapshot publication result")
	}
	epoch, err := redisInteger(result[0])
	if epoch == -1 {
		return PublishedSnapshot{}, false, errors.New("alarmd controlplane: snapshot revision collision")
	}
	if epoch == -2 {
		return PublishedSnapshot{}, false, ErrPublicationConflict
	}
	if epoch == -4 {
		return PublishedSnapshot{}, false, ErrPublicationOccurrenceCollision
	}
	if err != nil || epoch <= 0 {
		return PublishedSnapshot{}, false, errors.New("alarmd controlplane: invalid publication epoch")
	}
	created, err := redisInteger(result[1])
	if err != nil || (created != 0 && created != 1) {
		return PublishedSnapshot{}, false, errors.New("alarmd controlplane: invalid publication creation result")
	}
	snapshot := PublishedSnapshot{SchemaVersion: snapshotSchemaVersion,
		Publication: SnapshotPublicationRef{SnapshotRevision: catalog.SnapshotRevision, PublicationEpoch: uint64(epoch)},
		QueryGroups: append([]QueryGroup(nil), catalog.QueryGroups...)}
	// The Leader that published this catalog knows its content: fill the
	// catalog index from it so no activation of this publication reads the
	// objects back.
	if entries, err := indexFromCatalog(catalog); err == nil {
		repository.catalogIndex.replace(catalog.SnapshotRevision, entries)
	}
	return snapshot, created == 1, nil
}

const publishAuditScript = `
local latest = redis.call('GET', KEYS[1])
if not latest or latest ~= ARGV[1] then return 0 end
redis.call('PSETEX', KEYS[2], ARGV[2], ARGV[3])
redis.call('PSETEX', KEYS[3], ARGV[2], ARGV[4])
return 1
`

func (repository *RedisCatalogRepository) PublishAudit(ctx context.Context, audit SourceAuditState) error {
	if repository == nil || repository.client == nil || audit.ObservationID == "" || audit.Publication.validate() != nil {
		return errors.New("alarmd controlplane: incomplete source audit publication")
	}
	audit.SchemaVersion = snapshotSchemaVersion
	payload, err := json.Marshal(audit)
	if err != nil {
		return fmt.Errorf("alarmd controlplane: encode source audit: %w", err)
	}
	changed, err := repository.client.Eval(ctx, publishAuditScript, []string{
		repository.latestPublicationKey(), repository.auditKey(audit.ObservationID), repository.latestAuditKey(),
	}, publicationValue(audit.Publication), repository.ttl.Milliseconds(), payload, audit.ObservationID).Int()
	if err != nil {
		return fmt.Errorf("alarmd controlplane: publish source audit: %w", err)
	}
	if changed != 1 {
		return ErrPublicationConflict
	}
	return nil
}

func (repository *RedisCatalogRepository) validatePublicationOccurrence(
	ctx context.Context,
	publication SnapshotPublicationRef,
	current SnapshotPublicationRef,
) error {
	revision, err := repository.client.Get(ctx, repository.publicationKey(publication.PublicationEpoch)).Result()
	if errors.Is(err, redis.Nil) {
		revision, err = repository.client.HGet(
			ctx, repository.legacyPublicationsByEpochKey(), strconv.FormatUint(publication.PublicationEpoch, 10),
		).Result()
	}
	if errors.Is(err, redis.Nil) {
		if current != publication {
			return ErrSnapshotUnavailable
		}
	} else if err != nil {
		return activationDependencyIO(err)
	} else if revision != string(publication.SnapshotRevision) {
		return &PersistedSnapshotCorruptError{
			Err: errors.New("publication occurrence differs from Snapshot revision"),
		}
	}
	return nil
}

func (repository *RedisCatalogRepository) LoadLatestPublication(ctx context.Context) (SnapshotPublicationRef, error) {
	if repository == nil || repository.client == nil {
		return SnapshotPublicationRef{}, errors.New("alarmd controlplane: Redis catalog repository is required")
	}
	value, err := repository.client.Get(ctx, repository.latestPublicationKey()).Result()
	if errors.Is(err, redis.Nil) {
		return SnapshotPublicationRef{}, ErrSnapshotUnavailable
	}
	if err != nil {
		return SnapshotPublicationRef{}, err
	}
	parts := strings.Split(value, "\n")
	if len(parts) != 2 {
		return SnapshotPublicationRef{}, errors.New("alarmd controlplane: invalid latest publication reference")
	}
	epoch, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return SnapshotPublicationRef{}, errors.New("alarmd controlplane: invalid latest publication epoch")
	}
	reference := SnapshotPublicationRef{SnapshotRevision: execution.SnapshotRevision(parts[1]), PublicationEpoch: epoch}
	return reference, reference.validate()
}

// LoadQueryGroup reads one Query Group of one Snapshot revision from the
// object catalog: the manifest names its object and the output context of
// each of its Plans. It never falls forward to the latest publication. A
// revision whose manifest is gone reads as an unavailable snapshot; a
// Query Group the manifest does not name, or whose objects are gone, reads
// as an unavailable object.
func (repository *RedisCatalogRepository) LoadQueryGroup(ctx context.Context, revision execution.SnapshotRevision, identity execution.QueryGroupIdentity) (QueryGroup, error) {
	if identity == "" {
		return QueryGroup{}, errors.New("alarmd controlplane: query group identity is required")
	}
	// Served from this process where it can be. This is the Snapshot fallback
	// a Slot takes when its Segment names no object, or names content that
	// cannot be read, and it is the same whole-manifest read per Slot that the
	// freshness check was: quiet while every Segment is on the content path,
	// and the entire fleet at once on the day the object keys are gone.
	manifest, err := repository.retainedCatalogManifest(ctx, revision)
	if errors.Is(err, ErrCatalogManifestUnavailable) {
		return QueryGroup{}, ErrSnapshotUnavailable
	}
	if err != nil {
		return QueryGroup{}, err
	}
	var digest execution.ObjectDigest
	for _, entry := range manifest.QueryGroups {
		if entry.QueryGroup == identity {
			digest = entry.ObjectDigest
		}
	}
	if digest == "" {
		return QueryGroup{}, fmt.Errorf("%w: revision does not name Query Group %s", ErrCatalogObjectUnavailable, identity)
	}
	object, err := repository.LoadQueryGroupObject(ctx, digest)
	if err != nil {
		return QueryGroup{}, err
	}
	if object.Identity != identity {
		return QueryGroup{}, errors.New("alarmd controlplane: catalog object belongs to another Query Group")
	}
	named := make(map[execution.PlanIdentity]execution.OutputContextDigest, len(manifest.Plans))
	for _, entry := range manifest.Plans {
		named[entry.Plan] = entry.ContextDigest
	}
	refs := make([]execution.OutputContextRef, 0, len(object.Plans))
	for _, plan := range object.Plans {
		ref, ok := named[plan.Identity]
		if !ok {
			return QueryGroup{}, fmt.Errorf("%w: revision names no output context for Plan %s", ErrCatalogObjectUnavailable, plan.Identity.StrategyID)
		}
		refs = append(refs, execution.OutputContextRef{Plan: plan.Identity, Digest: ref})
	}
	contexts, err := repository.loadOutputContexts(ctx, refs)
	if err != nil {
		return QueryGroup{}, err
	}
	byPlan := make(map[execution.PlanIdentity]OutputContextObject, len(refs))
	for _, ref := range refs {
		byPlan[ref.Plan] = contexts[ref.Digest]
	}
	return AssembleQueryGroup(object, byPlan)
}

// loadPublishedQueryGroup reads one Query Group of a publication from the
// object catalog: its content entry from the manifest and the catalog
// index, then its object and output contexts, all served from the object
// cache when an activation already read them. The snapshot body is not
// consulted.
func (repository *RedisCatalogRepository) loadPublishedQueryGroup(
	ctx context.Context,
	publication SnapshotPublicationRef,
	identity execution.QueryGroupIdentity,
) (QueryGroup, error) {
	if publication.validate() != nil || identity == "" {
		return QueryGroup{}, errors.New("alarmd controlplane: complete publication and Query Group are required")
	}
	content, err := repository.LoadPublishedContent(ctx, publication)
	if err != nil {
		return QueryGroup{}, err
	}
	groups, err := repository.LoadContentQueryGroups(ctx, content, []execution.QueryGroupIdentity{identity})
	if err != nil {
		return QueryGroup{}, err
	}
	group, ok := groups[identity]
	if !ok {
		return QueryGroup{}, ErrCatalogObjectUnavailable
	}
	return group, nil
}

func (repository *RedisCatalogRepository) LoadLatestAudit(ctx context.Context) (SourceAuditState, error) {
	if repository == nil || repository.client == nil {
		return SourceAuditState{}, errors.New("alarmd controlplane: Redis catalog repository is required")
	}
	observationID, err := repository.client.Get(ctx, repository.latestAuditKey()).Result()
	if errors.Is(err, redis.Nil) {
		return SourceAuditState{}, ErrSnapshotUnavailable
	}
	if err != nil {
		return SourceAuditState{}, err
	}
	payload, err := repository.client.Get(ctx, repository.auditKey(observationID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return SourceAuditState{}, ErrSnapshotUnavailable
	}
	if err != nil {
		return SourceAuditState{}, err
	}
	var audit SourceAuditState
	if err := json.Unmarshal(payload, &audit); err != nil {
		return SourceAuditState{}, fmt.Errorf("alarmd controlplane: decode source audit: %w", err)
	}
	if audit.SchemaVersion != snapshotSchemaVersion || audit.ObservationID != observationID || audit.Publication.validate() != nil {
		return SourceAuditState{}, errors.New("alarmd controlplane: invalid persisted source audit")
	}
	return audit, nil
}

func (repository *RedisCatalogRepository) LoadActivation(ctx context.Context) (ActivationState, error) {
	entry, err := repository.loadParsedActivation(ctx)
	if err != nil {
		return ActivationState{}, err
	}
	return entry.cloneState(), nil
}

// loadParsedActivation still performs a live read per authorization: the small
// activation header and the activation length. The activation body itself is
// read only when the cache holds nothing for the observed header.
func (repository *RedisCatalogRepository) loadParsedActivation(ctx context.Context) (*parsedActivation, error) {
	if repository == nil || repository.client == nil {
		return nil, errors.New("alarmd controlplane: Redis catalog repository is required")
	}
	version, err := repository.readControlVersion(ctx)
	if err != nil {
		repository.clearActivationCaches()
		return nil, err
	}
	return repository.loadParsedActivationAt(ctx, version)
}

func (repository *RedisCatalogRepository) LoadActivations(ctx context.Context, request execution.PlanActivationRequest) (execution.PlanActivationResult, error) {
	if err := request.Contract.Validate(); err != nil || len(request.Plans) == 0 {
		return execution.PlanActivationResult{}, errors.New("alarmd controlplane: invalid activation request")
	}
	if repository == nil || repository.client == nil {
		return execution.PlanActivationResult{}, errors.New("alarmd controlplane: Redis catalog repository is required")
	}
	// One header probe covers both the activation and the Schedule timeline of
	// this authorization; both were persisted under that same header.
	version, err := repository.readControlVersion(ctx)
	if err != nil {
		repository.clearActivationCaches()
		return execution.PlanActivationResult{}, err
	}
	entry, err := repository.loadParsedActivationAt(ctx, version)
	if err != nil {
		return execution.PlanActivationResult{}, err
	}
	historical, err := repository.validateClosedHistoricalContract(ctx, request.Contract, entry.state.Current, version)
	if err != nil {
		return execution.PlanActivationResult{}, err
	}
	result := execution.PlanActivationResult{Contract: request.Contract, Facts: make([]execution.PlanActivationFact, 0, len(request.Plans))}
	for _, plan := range request.Plans {
		fact, found := entry.byPlan[plan]
		if historical || !found {
			fact = execution.PlanActivationFact{Plan: plan, Selection: execution.ActivationNone}
		}
		result.Facts = append(result.Facts, fact)
	}
	if err := result.Validate(request); err != nil {
		return execution.PlanActivationResult{}, err
	}
	return result, nil
}

func (repository *RedisCatalogRepository) validateClosedHistoricalContract(
	ctx context.Context,
	contractRef execution.FrozenExecutionContractRef,
	current SnapshotPublicationRef,
	version controlVersion,
) (bool, error) {
	timeline, err := repository.loadScheduleTimelineAt(ctx, contractRef.Slot.QueryGroup, version)
	if err != nil {
		return false, err
	}
	for _, segment := range timeline.Segments {
		schedule := segment.Schedule
		if !schedule.Segment.Contains(contractRef.Slot.EvaluationTime) {
			continue
		}
		if schedule.Segment.Start != contractRef.ScheduleSegmentStart ||
			schedule.Segment.ScheduleRevision != contractRef.ScheduleRevision ||
			schedule.Segment.Publication.SnapshotRevision != contractRef.SnapshotRevision ||
			schedule.Segment.QueryRevision != contractRef.QueryRevision {
			return false, errors.New("alarmd controlplane: activation request does not reference its persisted Schedule Segment")
		}
		// The current occurrence of a Query Group is its open Segment. It
		// need not name the current publication: a Query Group whose content
		// did not change keeps its Segment across publications, so naming
		// an older publication is the normal case, not a historical one.
		if schedule.Segment.End == nil {
			return false, nil
		}
		return true, nil
	}
	return false, errors.New("alarmd controlplane: activation request has no persisted Schedule Segment")
}

func validateActivationState(state ActivationState) error {
	if state.SchemaVersion != "" && state.SchemaVersion != activationSchemaVersion && state.SchemaVersion != legacyActivationSchemaVersion {
		return errors.New("alarmd controlplane: unsupported activation schema")
	}
	if state.SchemaVersion == activationSchemaVersion {
		if err := state.ActiveQGSetRef.validate(); err != nil {
			return err
		}
	}
	if state.RecordRevision == 0 || state.Current.validate() != nil {
		return errors.New("alarmd controlplane: incomplete activation state")
	}
	if state.Pending != nil && state.Pending.validate() != nil {
		return errors.New("alarmd controlplane: invalid pending snapshot reference")
	}
	if state.Pending != nil && *state.Pending == state.Current {
		return errors.New("alarmd controlplane: pending snapshot must differ from current")
	}
	seen := make(map[execution.PlanIdentity]struct{}, len(state.Plans))
	for _, record := range state.Plans {
		if _, duplicate := seen[record.Fact.Plan]; duplicate {
			return errors.New("alarmd controlplane: duplicate activation Plan")
		}
		seen[record.Fact.Plan] = struct{}{}
		if err := record.Fact.Plan.Validate(); err != nil || record.Publication.validate() != nil {
			return errors.New("alarmd controlplane: invalid activation Plan record")
		}
		switch record.Fact.Selection {
		case execution.ActivationCurrent:
			// A current record names the publication its Segment was opened
			// under. That is the current publication for a Plan whose Query
			// Group was cut or added by the latest activation, and an older
			// one for a Plan carried over because its content did not
			// change; it is never a newer one.
			if record.Publication.PublicationEpoch > state.Current.PublicationEpoch ||
				(record.Publication.PublicationEpoch == state.Current.PublicationEpoch && record.Publication != state.Current) {
				return errors.New("alarmd controlplane: current Plan references a publication past the current one")
			}
		case execution.ActivationPending:
			if state.Pending == nil || record.Publication != *state.Pending {
				return errors.New("alarmd controlplane: pending Plan references another snapshot")
			}
		case execution.ActivationNone:
			return errors.New("alarmd controlplane: persisted activation must omit unselected Plans")
		default:
			return errors.New("alarmd controlplane: invalid activation selection")
		}
		selected := record.Fact.Selected
		if selected.Identity != record.Fact.Plan || selected.StateGeneration == "" || selected.StateApplyEpoch == 0 || selected.ScheduleRevision == "" || selected.RequiredFullSlots == 0 {
			return errors.New("alarmd controlplane: incomplete activated Plan")
		}
	}
	seenDraining := make(map[execution.QueryGroupIdentity]struct{}, len(state.Draining))
	for _, draining := range state.Draining {
		if draining.QueryGroup == "" || draining.RetiredBoundary <= 0 {
			return errors.New("alarmd controlplane: invalid draining Query Group")
		}
		if _, duplicate := seenDraining[draining.QueryGroup]; duplicate {
			return errors.New("alarmd controlplane: duplicate draining Query Group")
		}
		seenDraining[draining.QueryGroup] = struct{}{}
	}
	return nil
}

func activationHeader(revision uint64, current SnapshotPublicationRef, pending *SnapshotPublicationRef) (string, error) {
	if revision == 0 {
		return "", nil
	}
	if current.validate() != nil || (pending != nil && pending.validate() != nil) {
		return "", errors.New("alarmd controlplane: invalid activation expectation")
	}
	pendingText := "-"
	if pending != nil {
		pendingText = fmt.Sprintf("%s@%d", pending.SnapshotRevision, pending.PublicationEpoch)
	}
	return fmt.Sprintf("%d|%s@%d|%s", revision, current.SnapshotRevision, current.PublicationEpoch, pendingText), nil
}

func redisInteger(value interface{}) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return 0, fmt.Errorf("unexpected Redis integer type %T", value)
	}
}

func publicationValue(reference SnapshotPublicationRef) string {
	return fmt.Sprintf("%d\n%s", reference.PublicationEpoch, reference.SnapshotRevision)
}

func (repository *RedisCatalogRepository) epochCounterKey() string {
	return repository.prefix + ":publication_epoch"
}
func (repository *RedisCatalogRepository) epochForRevisionKey(revision execution.SnapshotRevision) string {
	return repository.prefix + ":snapshot_epoch:" + string(revision)
}
func (repository *RedisCatalogRepository) latestPublicationKey() string {
	return repository.prefix + ":latest_publication"
}
func (repository *RedisCatalogRepository) publicationKeyPrefix() string {
	return repository.prefix + ":publication:"
}
func (repository *RedisCatalogRepository) publicationKey(epoch uint64) string {
	return repository.publicationKeyPrefix() + strconv.FormatUint(epoch, 10)
}
func (repository *RedisCatalogRepository) legacyPublicationsByEpochKey() string {
	return repository.prefix + ":publications_by_epoch"
}
func (repository *RedisCatalogRepository) auditKey(observation string) string {
	return repository.prefix + ":audit:" + observation
}
func (repository *RedisCatalogRepository) latestAuditKey() string {
	return repository.prefix + ":latest_audit"
}
func (repository *RedisCatalogRepository) activationHeaderKey() string {
	return repository.prefix + ":activation_header"
}
func (repository *RedisCatalogRepository) activationKey() string {
	return repository.prefix + ":activation"
}
func (repository *RedisCatalogRepository) activeQGSetKey(digest string) string {
	return repository.prefix + ":active_qg_set:" + digest
}

type SnapshotPublisher struct {
	repository *RedisCatalogRepository
}

func NewSnapshotPublisher(repository *RedisCatalogRepository) (*SnapshotPublisher, error) {
	if repository == nil {
		return nil, errors.New("alarmd controlplane: catalog repository is required")
	}
	return &SnapshotPublisher{repository: repository}, nil
}

func (publisher *SnapshotPublisher) Publish(ctx context.Context, catalog Catalog) (PublishedSnapshot, bool, error) {
	if publisher == nil || publisher.repository == nil || catalog.ObservationID == "" {
		return PublishedSnapshot{}, false, errors.New("alarmd controlplane: incomplete snapshot publish request")
	}
	snapshot, created, err := publisher.repository.PublishCatalog(ctx, catalog)
	return publisher.publishAudit(ctx, catalog, snapshot, created, err)
}

// PublishIfCurrent preserves the SourceReconciler's read-build-publish CAS.
// A stale caller returns ErrPublicationConflict and cannot update either the
// latest publication or its audit pointer.
func (publisher *SnapshotPublisher) PublishIfCurrent(
	ctx context.Context,
	expected SnapshotPublicationRef,
	catalog Catalog,
) (PublishedSnapshot, bool, error) {
	if publisher == nil || publisher.repository == nil || catalog.ObservationID == "" {
		return PublishedSnapshot{}, false, errors.New("alarmd controlplane: incomplete snapshot publish request")
	}
	snapshot, created, err := publisher.repository.PublishCatalogIfCurrent(ctx, expected, catalog)
	return publisher.publishAudit(ctx, catalog, snapshot, created, err)
}

func (publisher *SnapshotPublisher) restoreIfActivationCurrent(
	ctx context.Context,
	activation ActivationState,
	catalog Catalog,
) (PublishedSnapshot, bool, error) {
	if publisher == nil || publisher.repository == nil || catalog.ObservationID == "" {
		return PublishedSnapshot{}, false, errors.New("alarmd controlplane: incomplete Snapshot restore request")
	}
	snapshot, err := publisher.repository.restoreCatalogPublicationIfActivationCurrent(ctx, activation, catalog)
	return publisher.publishAudit(ctx, catalog, snapshot, false, err)
}

func (publisher *SnapshotPublisher) publishAudit(
	ctx context.Context,
	catalog Catalog,
	snapshot PublishedSnapshot,
	created bool,
	err error,
) (PublishedSnapshot, bool, error) {
	if err != nil {
		return PublishedSnapshot{}, false, err
	}
	audit := SourceAuditState{ObservationID: catalog.ObservationID, Publication: snapshot.Publication,
		Dispositions: append([]ObjectDisposition(nil), catalog.Dispositions...)}
	if err := publisher.repository.PublishAudit(ctx, audit); err != nil {
		return PublishedSnapshot{}, false, err
	}
	return snapshot, created, nil
}
