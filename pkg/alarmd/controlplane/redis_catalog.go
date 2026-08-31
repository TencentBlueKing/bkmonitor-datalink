package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const (
	snapshotSchemaVersion   = "alarmd-control-snapshot-v1"
	activationSchemaVersion = "alarmd-control-activation-v1"
)

var (
	ErrSnapshotUnavailable      = errors.New("alarmd controlplane: snapshot unavailable")
	ErrCatalogObjectUnavailable = errors.New("alarmd controlplane: catalog object unavailable")
	ErrActivationUnavailable    = errors.New("alarmd controlplane: activation unavailable")
	ErrActivationConflict       = errors.New("alarmd controlplane: activation conflict")
)

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

// ActivationState is storage for the one activation fact owned by module 02.
// This package supplies persistence and reads only; SnapshotPublisher never
// mutates activation.
type ActivationState struct {
	SchemaVersion  string                  `json:"schema_version"`
	RecordRevision uint64                  `json:"record_revision"`
	Current        SnapshotPublicationRef  `json:"current"`
	Pending        *SnapshotPublicationRef `json:"pending,omitempty"`
	Plans          []PlanActivationRecord  `json:"plans"`
}

type ActivationExpectation struct {
	RecordRevision uint64
	Current        SnapshotPublicationRef
	Pending        *SnapshotPublicationRef
}

type RedisCatalogRepository struct {
	client redis.Cmdable
	prefix string
	ttl    time.Duration
}

func NewRedisCatalogRepository(client redis.Cmdable, prefix string, ttl time.Duration) (*RedisCatalogRepository, error) {
	if client == nil || prefix == "" || strings.ContainsAny(prefix, "{} \t\r\n") || ttl <= 0 {
		return nil, errors.New("alarmd controlplane: invalid Redis catalog repository")
	}
	return &RedisCatalogRepository{client: client, prefix: prefix, ttl: ttl}, nil
}

func (repository *RedisCatalogRepository) Ping(ctx context.Context) error {
	if repository == nil || repository.client == nil {
		return errors.New("alarmd controlplane: Redis catalog repository is required")
	}
	return repository.client.Ping(ctx).Err()
}

const publishSnapshotScript = `
local snapshot = redis.call('GET', KEYS[3])
if snapshot and snapshot ~= ARGV[1] then
  return {-1, -1}
end
local epoch = redis.call('GET', KEYS[2])
local created = 0
if not epoch then
  epoch = redis.call('INCR', KEYS[1])
  redis.call('PSETEX', KEYS[2], ARGV[2], tostring(epoch))
  created = 1
else
  redis.call('PEXPIRE', KEYS[2], ARGV[2])
end
if snapshot then
  redis.call('PEXPIRE', KEYS[3], ARGV[2])
else
  redis.call('PSETEX', KEYS[3], ARGV[2], ARGV[1])
end
local latest = redis.call('GET', KEYS[4])
local latest_epoch = 0
if latest then
  latest_epoch = tonumber(string.match(latest, '^(%d+)')) or 0
end
if tonumber(epoch) >= latest_epoch then
  redis.call('PSETEX', KEYS[4], ARGV[2], tostring(epoch) .. '\n' .. ARGV[3])
end
return {tonumber(epoch), created}
`

func (repository *RedisCatalogRepository) PublishCatalog(ctx context.Context, catalog Catalog) (PublishedSnapshot, bool, error) {
	if repository == nil || repository.client == nil || catalog.SnapshotRevision == "" || catalog.QueryGroups == nil {
		return PublishedSnapshot{}, false, errors.New("alarmd controlplane: incomplete catalog publication")
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
	result, err := repository.client.Eval(ctx, publishSnapshotScript, []string{
		repository.epochCounterKey(), repository.epochForRevisionKey(catalog.SnapshotRevision),
		repository.snapshotKey(catalog.SnapshotRevision), repository.latestPublicationKey(),
	}, payload, repository.ttl.Milliseconds(), string(catalog.SnapshotRevision)).Slice()
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

func (repository *RedisCatalogRepository) PublishAudit(ctx context.Context, audit SourceAuditState) error {
	if repository == nil || repository.client == nil || audit.ObservationID == "" || audit.Publication.validate() != nil {
		return errors.New("alarmd controlplane: incomplete source audit publication")
	}
	audit.SchemaVersion = snapshotSchemaVersion
	payload, err := json.Marshal(audit)
	if err != nil {
		return fmt.Errorf("alarmd controlplane: encode source audit: %w", err)
	}
	_, err = repository.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, repository.auditKey(audit.ObservationID), payload, repository.ttl)
		pipe.Set(ctx, repository.latestAuditKey(), audit.ObservationID, repository.ttl)
		return nil
	})
	if err != nil {
		return fmt.Errorf("alarmd controlplane: publish source audit: %w", err)
	}
	return nil
}

func (repository *RedisCatalogRepository) LoadSnapshot(ctx context.Context, revision execution.SnapshotRevision) (PublishedSnapshot, error) {
	if repository == nil || repository.client == nil || revision == "" {
		return PublishedSnapshot{}, errors.New("alarmd controlplane: snapshot revision is required")
	}
	values, err := repository.client.MGet(ctx, repository.snapshotKey(revision), repository.epochForRevisionKey(revision)).Result()
	if err != nil {
		return PublishedSnapshot{}, err
	}
	if len(values) != 2 || values[0] == nil || values[1] == nil {
		return PublishedSnapshot{}, ErrSnapshotUnavailable
	}
	payload, ok := legacyRedisBytes(values[0])
	if !ok {
		return PublishedSnapshot{}, errors.New("alarmd controlplane: invalid persisted snapshot payload")
	}
	var content struct {
		SchemaVersion    string       `json:"schema_version"`
		SnapshotRevision string       `json:"snapshot_revision"`
		QueryGroups      []QueryGroup `json:"query_groups"`
	}
	if err := json.Unmarshal(payload, &content); err != nil {
		return PublishedSnapshot{}, fmt.Errorf("alarmd controlplane: decode snapshot: %w", err)
	}
	epochText, ok := values[1].(string)
	if !ok {
		return PublishedSnapshot{}, errors.New("alarmd controlplane: invalid persisted publication epoch")
	}
	epoch, err := strconv.ParseUint(epochText, 10, 64)
	if err != nil || epoch == 0 || content.SchemaVersion != snapshotSchemaVersion || content.SnapshotRevision != string(revision) || content.QueryGroups == nil {
		return PublishedSnapshot{}, errors.New("alarmd controlplane: invalid persisted snapshot")
	}
	derivedRevision, err := deriveSnapshotRevision(content.QueryGroups)
	if err != nil || derivedRevision != revision {
		return PublishedSnapshot{}, errors.New("alarmd controlplane: persisted snapshot content does not match revision")
	}
	return PublishedSnapshot{SchemaVersion: content.SchemaVersion,
		Publication: SnapshotPublicationRef{SnapshotRevision: revision, PublicationEpoch: epoch}, QueryGroups: content.QueryGroups}, nil
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
	snapshot, err := repository.LoadSnapshot(ctx, revision)
	if err != nil {
		return QueryGroup{}, err
	}
	for _, group := range snapshot.QueryGroups {
		if group.Identity == identity {
			return group, nil
		}
	}
	return QueryGroup{}, ErrCatalogObjectUnavailable
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
	if repository == nil || repository.client == nil {
		return ActivationState{}, errors.New("alarmd controlplane: Redis catalog repository is required")
	}
	payload, err := repository.client.Get(ctx, repository.activationKey()).Bytes()
	if errors.Is(err, redis.Nil) {
		return ActivationState{}, ErrActivationUnavailable
	}
	if err != nil {
		return ActivationState{}, err
	}
	var state ActivationState
	if err := json.Unmarshal(payload, &state); err != nil {
		return ActivationState{}, fmt.Errorf("alarmd controlplane: decode activation: %w", err)
	}
	if err := validateActivationState(state); err != nil {
		return ActivationState{}, err
	}
	return state, nil
}

func (repository *RedisCatalogRepository) LoadActivations(ctx context.Context, request execution.PlanActivationRequest) (execution.PlanActivationResult, error) {
	if err := request.Contract.Validate(); err != nil || len(request.Plans) == 0 {
		return execution.PlanActivationResult{}, errors.New("alarmd controlplane: invalid activation request")
	}
	state, err := repository.LoadActivation(ctx)
	if err != nil {
		return execution.PlanActivationResult{}, err
	}
	byPlan := make(map[execution.PlanIdentity]execution.PlanActivationFact, len(state.Plans))
	for _, record := range state.Plans {
		byPlan[record.Fact.Plan] = record.Fact
	}
	result := execution.PlanActivationResult{Contract: request.Contract, Facts: make([]execution.PlanActivationFact, 0, len(request.Plans))}
	for _, plan := range request.Plans {
		fact, found := byPlan[plan]
		if !found {
			fact = execution.PlanActivationFact{Plan: plan, Selection: execution.ActivationNone}
		}
		result.Facts = append(result.Facts, fact)
	}
	if err := result.Validate(request); err != nil {
		return execution.PlanActivationResult{}, err
	}
	return result, nil
}

func validateActivationState(state ActivationState) error {
	if state.SchemaVersion != "" && state.SchemaVersion != activationSchemaVersion {
		return errors.New("alarmd controlplane: unsupported activation schema")
	}
	if state.RecordRevision == 0 || state.Current.validate() != nil || len(state.Plans) == 0 {
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
			if record.Publication != state.Current {
				return errors.New("alarmd controlplane: current Plan references another snapshot")
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
