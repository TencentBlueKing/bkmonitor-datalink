package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
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
	client                     redis.Cmdable
	prefix                     string
	ttl                        time.Duration
	snapshotCache              *verifiedSnapshotCache
	snapshotFlights            snapshotReadFlights
	activationCache            parsedActivationCache
	objectCatalog              objectCatalogState
	objectCache                *objectReadCache
	objectFlights              objectReadFlights
	controlCache               *controlReadCache
	controlReads               controlReadCounters
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
	snapshotAdmission      SnapshotMemoryAdmission
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

// ConfigureSnapshotMemory is called before starting repository users.
func (repository *RedisCatalogRepository) ConfigureSnapshotMemory(admit SnapshotMemoryAdmission, size func(any) uint64) {
	repository.snapshotAdmission = admit
	repository.snapshotCache.admit, repository.snapshotCache.objectBytes = admit, size
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
		snapshotCache: newVerifiedSnapshotCache(verifiedSnapshotCacheMaxEntries, verifiedSnapshotCacheMaxBytes),
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

const renewCurrentActivationObjectsScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
if redis.call('GET', KEYS[2]) ~= ARGV[5] or redis.call('GET', KEYS[3]) ~= ARGV[3] or
   redis.call('GET', KEYS[4]) ~= ARGV[4] or redis.call('GET', KEYS[5]) ~= ARGV[6] then return -1 end
redis.call('PEXPIRE', KEYS[2], ARGV[2])
redis.call('PEXPIRE', KEYS[3], ARGV[2])
redis.call('PEXPIRE', KEYS[4], ARGV[2])
redis.call('PEXPIRE', KEYS[5], ARGV[2])
return 1
`

// RenewCurrentActivationObjects renews only the complete Snapshot occurrence
// and Active Set named by the same Activation header, together with the
// Schedule timelines that Activation still references. A concurrent cutover
// cannot renew stale facts.
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
	snapshotPayload, err := repository.client.Get(ctx, repository.snapshotKey(state.Current.SnapshotRevision)).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrSnapshotUnavailable
	}
	if err != nil {
		return err
	}
	activePayload, err := repository.client.Get(ctx, repository.activeQGSetKey(state.ActiveQGSetRef.Digest)).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrSnapshotUnavailable
	}
	if err != nil {
		return err
	}
	if _, err := repository.LoadPublishedSnapshot(ctx, state.Current); err != nil {
		return err
	}
	groups, err := repository.LoadActiveQueryGroupSet(ctx, state.ActiveQGSetRef)
	if err != nil {
		return err
	}
	result, err := repository.client.Eval(ctx, renewCurrentActivationObjectsScript, []string{
		repository.activationHeaderKey(),
		repository.snapshotKey(state.Current.SnapshotRevision),
		repository.epochForRevisionKey(state.Current.SnapshotRevision),
		repository.publicationKey(state.Current.PublicationEpoch),
		repository.activeQGSetKey(state.ActiveQGSetRef.Digest),
	}, header, repository.ttl.Milliseconds(), strconv.FormatUint(state.Current.PublicationEpoch, 10), string(state.Current.SnapshotRevision),
		snapshotPayload, activePayload).Int()
	if err != nil {
		return fmt.Errorf("alarmd controlplane: renew current activation objects: %w", err)
	}
	switch result {
	case 1:
		// The Schedule timelines of the renewed Active Set and of the Draining
		// projection are renewed only after the guarded CAS proved that the
		// state read above is still the current one.
		if err := repository.renewScheduleTimelines(ctx, groups, state.Draining); err != nil {
			return err
		}
		repository.renewObjectCatalog(ctx, state.Current.SnapshotRevision)
		metricResult = "success"
		queryGroups, objectBytes = len(groups), len(activePayload)
		return nil
	case 0:
		return ErrActivationConflict
	default:
		return ErrSnapshotUnavailable
	}
}

const publishSnapshotScript = `
local function occurrence_key(epoch)
  return KEYS[5] .. tostring(epoch)
end
local latest = redis.call('GET', KEYS[4])
if ARGV[4] == '' then
  if latest then return {-2, -2} end
elseif not latest or latest ~= ARGV[4] then
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
local snapshot = redis.call('GET', KEYS[3])
if snapshot and snapshot ~= ARGV[1] then
  return {-1, -1}
end
if snapshot then
  redis.call('PEXPIRE', KEYS[3], ARGV[2])
else
  redis.call('PSETEX', KEYS[3], ARGV[2], ARGV[1])
end
if latest then
  redis.call('SET', occurrence_key(latest_epoch), latest_revision, 'PX', ARGV[2], 'NX')
end
local previous_epoch = redis.call('GET', KEYS[2])
if previous_epoch then
  redis.call('SET', occurrence_key(previous_epoch), ARGV[3], 'PX', ARGV[2], 'NX')
end
if latest then
  if latest_revision == ARGV[3] then
    redis.call('PSETEX', occurrence_key(latest_epoch), ARGV[2], latest_revision)
    redis.call('PSETEX', KEYS[2], ARGV[2], latest_epoch)
    redis.call('PEXPIRE', KEYS[4], ARGV[2])
    return {tonumber(latest_epoch), 0}
  end
end
local epoch = redis.call('INCR', KEYS[1])
redis.call('PSETEX', occurrence_key(epoch), ARGV[2], ARGV[3])
redis.call('PSETEX', KEYS[2], ARGV[2], tostring(epoch))
redis.call('PSETEX', KEYS[4], ARGV[2], tostring(epoch) .. '\n' .. ARGV[3])
return {tonumber(epoch), 1}
`

const restoreSnapshotPublicationScript = `
local header = redis.call('GET', KEYS[1])
if not header or header ~= ARGV[1] then return 0 end
local latest = redis.call('GET', KEYS[2])
if ARGV[7] == '' then
  if latest then return 0 end
elseif not latest or latest ~= ARGV[7] then
  return 0
end
local snapshot = redis.call('GET', KEYS[3])
if snapshot and snapshot ~= ARGV[2] then return -1 end
local occurrence = redis.call('GET', KEYS[5])
if occurrence and occurrence ~= ARGV[6] then return -2 end
redis.call('PSETEX', KEYS[3], ARGV[3], ARGV[2])
redis.call('PSETEX', KEYS[4], ARGV[3], ARGV[4])
redis.call('PSETEX', KEYS[5], ARGV[3], ARGV[6])
redis.call('PSETEX', KEYS[2], ARGV[3], ARGV[5])
return 1
`

// restoreCatalogPublicationIfActivationCurrent recreates expired immutable
// Catalog facts only when the persistent Activation still selects the exact
// same content-addressed Snapshot occurrence. It does not create a new epoch
// or mutate Schedule/Activation provenance.
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
	revision, err := deriveSnapshotRevision(catalog.QueryGroups)
	if err != nil || revision != catalog.SnapshotRevision {
		return PublishedSnapshot{}, errors.New("alarmd controlplane: restored Snapshot revision does not match Catalog")
	}
	content := struct {
		SchemaVersion    string       `json:"schema_version"`
		SnapshotRevision string       `json:"snapshot_revision"`
		QueryGroups      []QueryGroup `json:"query_groups"`
	}{SchemaVersion: snapshotSchemaVersion, SnapshotRevision: string(catalog.SnapshotRevision), QueryGroups: catalog.QueryGroups}
	payload, err := json.Marshal(content)
	if err != nil {
		return PublishedSnapshot{}, fmt.Errorf("alarmd controlplane: encode restored Snapshot: %w", err)
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
	changed, err := repository.client.Eval(ctx, restoreSnapshotPublicationScript, []string{
		repository.activationHeaderKey(), repository.latestPublicationKey(),
		repository.snapshotKey(catalog.SnapshotRevision), repository.epochForRevisionKey(catalog.SnapshotRevision),
		repository.publicationKey(activation.Current.PublicationEpoch),
	}, header, payload, repository.ttl.Milliseconds(), epoch, publicationValue(activation.Current),
		string(catalog.SnapshotRevision), latest).Int()
	if err != nil {
		return PublishedSnapshot{}, fmt.Errorf("alarmd controlplane: restore expired Snapshot: %w", err)
	}
	if changed == -1 {
		return PublishedSnapshot{}, errors.New("alarmd controlplane: restored Snapshot revision collision")
	}
	if changed == -2 {
		return PublishedSnapshot{}, ErrPublicationOccurrenceCollision
	}
	if changed != 1 {
		return PublishedSnapshot{}, ErrPublicationConflict
	}
	return PublishedSnapshot{SchemaVersion: snapshotSchemaVersion, Publication: activation.Current,
		QueryGroups: append([]QueryGroup(nil), catalog.QueryGroups...)}, nil
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
	content := struct {
		SchemaVersion    string       `json:"schema_version"`
		SnapshotRevision string       `json:"snapshot_revision"`
		QueryGroups      []QueryGroup `json:"query_groups"`
	}{SchemaVersion: snapshotSchemaVersion, SnapshotRevision: string(catalog.SnapshotRevision), QueryGroups: catalog.QueryGroups}
	payload, err := json.Marshal(content)
	if err != nil {
		return PublishedSnapshot{}, false, fmt.Errorf("alarmd controlplane: encode snapshot: %w", err)
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
		repository.snapshotKey(catalog.SnapshotRevision), repository.latestPublicationKey(), repository.publicationKeyPrefix(),
	}, payload, repository.ttl.Milliseconds(), string(catalog.SnapshotRevision), expectedValue).Slice()
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

func (repository *RedisCatalogRepository) LoadSnapshot(ctx context.Context, revision execution.SnapshotRevision) (PublishedSnapshot, error) {
	if repository == nil || repository.client == nil || revision == "" {
		return PublishedSnapshot{}, errors.New("alarmd controlplane: snapshot revision is required")
	}
	payload, epoch, allocation, err := repository.loadAdmittedSnapshotPayload(ctx, revision)
	defer allocation.release()
	if err != nil {
		return PublishedSnapshot{}, err
	}
	snapshot, err := repository.snapshotCache.loadSnapshot(ctx, revision, payload, epoch, allocation)
	if err != nil {
		return PublishedSnapshot{}, err
	}
	snapshot.Publication = SnapshotPublicationRef{SnapshotRevision: revision, PublicationEpoch: epoch}
	return snapshot, nil
}

func (repository *RedisCatalogRepository) loadSnapshotPayload(
	ctx context.Context,
	revision execution.SnapshotRevision,
) (string, uint64, error) {
	values, err := repository.client.MGet(ctx, repository.snapshotKey(revision), repository.epochForRevisionKey(revision)).Result()
	if err != nil {
		return "", 0, activationDependencyIO(err)
	}
	return decodeSnapshotRead(values)
}

func decodeSnapshotRead(values []interface{}) (string, uint64, error) {
	if len(values) != 2 || values[0] == nil || values[1] == nil {
		return "", 0, ErrSnapshotUnavailable
	}
	// go-redis returns immutable strings. Keep that representation through a
	// warm verified-cache lookup rather than copying the entire Snapshot.
	var payload string
	switch value := values[0].(type) {
	case string:
		payload = value
	case []byte:
		payload = string(value)
	default:
		return "", 0, &PersistedSnapshotCorruptError{Err: errors.New("invalid payload")}
	}
	epochText, ok := values[1].(string)
	if !ok {
		return "", 0, &PersistedSnapshotCorruptError{Err: errors.New("invalid publication epoch")}
	}
	epoch, err := strconv.ParseUint(epochText, 10, 64)
	if err != nil || epoch == 0 {
		return "", 0, &PersistedSnapshotCorruptError{Err: errors.New("invalid publication epoch")}
	}
	return payload, epoch, nil
}

// LoadPublishedSnapshot resolves one exact publication occurrence without
// falling forward to another epoch that reused the same Snapshot content.
func (repository *RedisCatalogRepository) LoadPublishedSnapshot(
	ctx context.Context,
	publication SnapshotPublicationRef,
) (PublishedSnapshot, error) {
	if publication.validate() != nil {
		return PublishedSnapshot{}, errors.New("alarmd controlplane: complete publication is required")
	}
	snapshot, err := repository.LoadSnapshot(ctx, publication.SnapshotRevision)
	if err != nil {
		return PublishedSnapshot{}, err
	}
	if err := repository.validatePublicationOccurrence(ctx, publication, snapshot.Publication); err != nil {
		return PublishedSnapshot{}, err
	}
	snapshot.Publication = publication
	return snapshot, nil
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

// LoadQueryGroup resolves only immutable content from the explicitly selected
// Snapshot. It never falls forward to the latest publication.
func (repository *RedisCatalogRepository) LoadQueryGroup(ctx context.Context, revision execution.SnapshotRevision, identity execution.QueryGroupIdentity) (QueryGroup, error) {
	if identity == "" {
		return QueryGroup{}, errors.New("alarmd controlplane: query group identity is required")
	}
	payload, epoch, allocation, err := repository.loadScopedSnapshotPayload(ctx, revision, identity)
	defer allocation.release()
	if err != nil {
		clearSnapshotScope(ctx)
		return QueryGroup{}, err
	}
	entry, _, err := repository.snapshotCache.load(ctx, revision, payload, epoch, allocation)
	defer entry.allocation.release()
	if err != nil {
		clearSnapshotScope(ctx)
		return QueryGroup{}, err
	}
	group, err := decodeCachedQueryGroup(entry, identity)
	if err != nil {
		clearSnapshotScope(ctx)
		return QueryGroup{}, err
	}
	repository.retainScopedSnapshot(ctx, revision, identity, entry.payload, epoch, entry.allocation)
	return group, nil
}

func (repository *RedisCatalogRepository) loadPublishedQueryGroup(
	ctx context.Context,
	publication SnapshotPublicationRef,
	identity execution.QueryGroupIdentity,
) (QueryGroup, error) {
	if publication.validate() != nil || identity == "" {
		return QueryGroup{}, errors.New("alarmd controlplane: complete publication and Query Group are required")
	}
	payload, epoch, allocation, err := repository.loadScopedSnapshotPayload(ctx, publication.SnapshotRevision, identity)
	defer allocation.release()
	if err != nil {
		clearSnapshotScope(ctx)
		return QueryGroup{}, err
	}
	entry, _, err := repository.snapshotCache.load(ctx, publication.SnapshotRevision, payload, epoch, allocation)
	defer entry.allocation.release()
	if err != nil {
		clearSnapshotScope(ctx)
		return QueryGroup{}, err
	}
	current := SnapshotPublicationRef{SnapshotRevision: publication.SnapshotRevision, PublicationEpoch: epoch}
	if err := repository.validatePublicationOccurrence(ctx, publication, current); err != nil {
		clearSnapshotScope(ctx)
		return QueryGroup{}, err
	}
	group, err := decodeCachedQueryGroup(entry, identity)
	if err != nil {
		clearSnapshotScope(ctx)
		return QueryGroup{}, err
	}
	repository.retainScopedSnapshot(ctx, publication.SnapshotRevision, identity, entry.payload, epoch, entry.allocation)
	return group, nil
}

// LoadPlan resolves a Plan by its stable identity inside one frozen Snapshot.
func (repository *RedisCatalogRepository) LoadPlan(ctx context.Context, revision execution.SnapshotRevision, identity execution.PlanIdentity) (FrozenPlan, error) {
	if err := identity.Validate(); err != nil {
		return FrozenPlan{}, err
	}
	snapshot, err := repository.LoadSnapshot(ctx, revision)
	if err != nil {
		return FrozenPlan{}, err
	}
	for _, group := range snapshot.QueryGroups {
		for _, plan := range group.Plans {
			if plan.Identity == identity {
				return plan, nil
			}
		}
	}
	return FrozenPlan{}, ErrCatalogObjectUnavailable
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
func (repository *RedisCatalogRepository) snapshotKey(revision execution.SnapshotRevision) string {
	return repository.prefix + ":snapshot:" + string(revision)
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
