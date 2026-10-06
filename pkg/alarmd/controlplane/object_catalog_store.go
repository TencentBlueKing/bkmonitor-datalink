// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The object catalog is the publication broken into the pieces a Worker
// reads: one immutable object per Query Group under its ObjectDigest, one
// immutable output context per Plan under its OutputContextDigest, and one
// manifest per publication naming which digest each identity resolves to.
// Objects are content-addressed, so a publication that changes k Query Groups
// writes k objects and one manifest, and the objects of every other Query
// Group are found already stored and left alone.
//
// While the whole Snapshot is still what execution reads, the catalog is
// written next to it and nothing consumes it; a failure to write it is
// counted and the next refresh tries again, and it never fails the
// publication. Every key carries the Catalog TTL, and the renewal that keeps
// the current Snapshot alive renews the objects the current manifest names.
const (
	catalogManifestSchemaVersion = "alarmd-catalog-manifest-v1"
	objectCatalogBatch           = 512
)

// ErrCatalogManifestUnavailable reports that the manifest of a publication is
// not stored: either it was never written, or it expired.
var ErrCatalogManifestUnavailable = errors.New("alarmd controlplane: catalog manifest unavailable")

// ErrCatalogManifestCollision reports a manifest stored under a revision with
// different bytes from the one being written. The manifest is a pure function
// of the catalog and the revision is a digest of the catalog, so this cannot
// happen through this code; it is the same guard the Snapshot has.
var ErrCatalogManifestCollision = errors.New("alarmd controlplane: catalog manifest collision")

// CatalogManifest names the objects of one publication. Entries are sorted by
// identity so the encoding is a function of the content alone.
type CatalogManifest struct {
	SchemaVersion    string                     `json:"schema_version"`
	SnapshotRevision execution.SnapshotRevision `json:"snapshot_revision"`
	QueryGroups      []ManifestQueryGroup       `json:"query_groups"`
	Plans            []ManifestPlan             `json:"plans"`
}

type ManifestQueryGroup struct {
	QueryGroup   execution.QueryGroupIdentity `json:"query_group"`
	ObjectDigest execution.ObjectDigest       `json:"object_digest"`
}

type ManifestPlan struct {
	Plan          execution.PlanIdentity        `json:"plan"`
	ContextDigest execution.OutputContextDigest `json:"output_context_digest"`
}

type objectCatalogContent struct {
	manifest        CatalogManifest
	manifestPayload []byte
	objects         map[execution.ObjectDigest][]byte
	contexts        map[execution.OutputContextDigest][]byte
	// noDataPlans is how many Plans detect no-data according to the bytes this
	// publication writes, read back out of those bytes rather than counted
	// off the Catalog they were built from. The two should be the same number
	// and the whole reason to take this one is that saying so is not evidence.
	noDataPlans int
}

// objectCatalogState remembers, for the revision this process last wrote or
// renewed, which digests it knows to be stored. A write for a new revision
// asks Redis only about the digests outside that set, and a renewal that
// finds a key gone forgets the revision so the next write asks about
// everything again.
type objectCatalogState struct {
	mu       sync.Mutex
	revision execution.SnapshotRevision
	objects  map[execution.ObjectDigest]struct{}
	contexts map[execution.OutputContextDigest]struct{}
}

func (state *objectCatalogState) snapshot() (execution.SnapshotRevision, map[execution.ObjectDigest]struct{}, map[execution.OutputContextDigest]struct{}) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.revision, state.objects, state.contexts
}

func (state *objectCatalogState) replace(revision execution.SnapshotRevision, objects map[execution.ObjectDigest]struct{}, contexts map[execution.OutputContextDigest]struct{}) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.revision, state.objects, state.contexts = revision, objects, contexts
}

func (state *objectCatalogState) forget() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.revision, state.objects, state.contexts = "", nil, nil
}

func (repository *RedisCatalogRepository) queryGroupObjectKey(digest execution.ObjectDigest) string {
	return repository.prefix + ":qgobj:" + string(digest)
}

func (repository *RedisCatalogRepository) outputContextKey(digest execution.OutputContextDigest) string {
	return repository.prefix + ":outctx:" + string(digest)
}

func (repository *RedisCatalogRepository) catalogManifestKey(revision execution.SnapshotRevision) string {
	return repository.prefix + ":manifest:" + string(revision)
}

// objectRetentionKey holds, per publication, how long that publication's
// objects and output contexts are kept (Catalog.ObjectRetention), in
// milliseconds. It sits beside the manifest rather than in it because the
// manifest is decoded strictly. Absent means the catalog TTL.
func (repository *RedisCatalogRepository) objectRetentionKey(revision execution.SnapshotRevision) string {
	return repository.prefix + ":object_retention:" + string(revision)
}

// objectTTL is the TTL content objects are written and renewed with: the
// publication's own retention where it asks for longer than the catalog TTL,
// the catalog TTL otherwise. Never shorter: every other key of the catalog is
// kept for the catalog TTL, and an object outliving them costs nothing but
// its bytes.
func (repository *RedisCatalogRepository) objectTTL(retention time.Duration) time.Duration {
	if retention > repository.ttl {
		return retention
	}
	return repository.ttl
}

// storedObjectRetention reads a publication's object retention. Absent or
// unreadable reads as the catalog TTL, which is what every publication
// before this key had: the renewal it feeds is advisory, and a lost
// retention costs a long Plan's superseded objects their extra life, never
// a renewal.
func (repository *RedisCatalogRepository) storedObjectRetention(ctx context.Context, revision execution.SnapshotRevision) time.Duration {
	value, err := repository.client.Get(ctx, repository.objectRetentionKey(revision)).Result()
	if err != nil {
		return repository.ttl
	}
	millis, err := strconv.ParseInt(value, 10, 64)
	if err != nil || millis <= 0 {
		return repository.ttl
	}
	return repository.objectTTL(time.Duration(millis) * time.Millisecond)
}

// buildObjectCatalogContent derives every object, every output context and
// the manifest of one catalog. Objects are stored as the canonical bytes their
// digest was computed over, so a reader can verify what it reads by hashing
// it.
func buildObjectCatalogContent(catalog Catalog) (objectCatalogContent, error) {
	content := objectCatalogContent{
		manifest: CatalogManifest{SchemaVersion: catalogManifestSchemaVersion, SnapshotRevision: catalog.SnapshotRevision,
			QueryGroups: make([]ManifestQueryGroup, 0, len(catalog.QueryGroups)), Plans: make([]ManifestPlan, 0, len(catalog.QueryGroups))},
		objects:  make(map[execution.ObjectDigest][]byte, len(catalog.QueryGroups)),
		contexts: make(map[execution.OutputContextDigest][]byte, len(catalog.QueryGroups)),
	}
	for _, group := range catalog.QueryGroups {
		object := BuildQueryGroupObject(group)
		payload, err := contract.CanonicalJSONV2(object)
		if err != nil {
			return objectCatalogContent{}, fmt.Errorf("alarmd controlplane: encode Query Group object: %w", err)
		}
		published, err := noDataPlansInPayload(payload)
		if err != nil {
			return objectCatalogContent{}, err
		}
		content.noDataPlans += published
		digest, err := contract.DeriveCanonicalDigestV2OverCanonical(object.ContractVersion, payload)
		if err != nil {
			return objectCatalogContent{}, err
		}
		content.manifest.QueryGroups = append(content.manifest.QueryGroups, ManifestQueryGroup{QueryGroup: group.Identity, ObjectDigest: execution.ObjectDigest(digest)})
		content.objects[execution.ObjectDigest(digest)] = payload
		for _, plan := range group.Plans {
			payload, err := contract.CanonicalJSONV2(BuildOutputContext(plan))
			if err != nil {
				return objectCatalogContent{}, fmt.Errorf("alarmd controlplane: encode output context: %w", err)
			}
			digest, err := contract.DeriveCanonicalDigestV2OverCanonical(outputContextContractVersion, payload)
			if err != nil {
				return objectCatalogContent{}, err
			}
			content.manifest.Plans = append(content.manifest.Plans, ManifestPlan{Plan: plan.Identity, ContextDigest: execution.OutputContextDigest(digest)})
			content.contexts[execution.OutputContextDigest(digest)] = payload
		}
	}
	sort.Slice(content.manifest.QueryGroups, func(i, j int) bool {
		return content.manifest.QueryGroups[i].QueryGroup < content.manifest.QueryGroups[j].QueryGroup
	})
	sort.Slice(content.manifest.Plans, func(i, j int) bool {
		return lessPlanIdentity(content.manifest.Plans[i].Plan, content.manifest.Plans[j].Plan)
	})
	payload, err := contract.CanonicalJSONV2(content.manifest)
	if err != nil {
		return objectCatalogContent{}, fmt.Errorf("alarmd controlplane: encode catalog manifest: %w", err)
	}
	content.manifestPayload = payload
	return content, nil
}

// writeCatalogManifestScript stores the manifest under its revision, or
// renews it when the same bytes are already there, and refuses different
// bytes under the same revision.
const writeCatalogManifestScript = `
local current = redis.call('GET', KEYS[1])
if current and current ~= ARGV[1] then return -1 end
if current then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
else
  redis.call('PSETEX', KEYS[1], ARGV[2], ARGV[1])
end
return 1
`

// ensureObjectCatalog writes the objects and the manifest of a catalog that
// are not stored yet. It is idempotent per revision: a second call for the
// revision this process last wrote does nothing, and a call for a new
// revision asks Redis only about the digests the previous revision did not
// already prove present. Failure is returned, and the publication that
// called does not publish: a Query Group whose content does not change
// keeps its Segment across publications, past the retention of the Snapshot
// that Segment was opened under, so the objects are what execution reads
// and a publication without them would run Workers into nothing.
func (repository *RedisCatalogRepository) ensureObjectCatalog(ctx context.Context, catalog Catalog) error {
	if repository == nil || repository.client == nil || catalog.SnapshotRevision == "" {
		return errors.New("alarmd controlplane: object catalog write requires a catalog revision")
	}
	if revision, _, _ := repository.objectCatalog.snapshot(); revision == catalog.SnapshotRevision {
		return nil
	}
	started := time.Now()
	facts := &observability.ObjectCatalogFacts{Operation: "write", Result: "failure", QueryGroups: len(catalog.QueryGroups)}
	// Counted here because this is the one place a whole publication is in
	// hand: how much of the fleet a value-list split could be expressed for
	// at all, which is the number that decides whether hashing is the main
	// road (decision-020 section 4.7.2). One pass over what is already held.
	shardability := Shardability(catalog.QueryGroups)
	defer func() {
		facts.Duration = time.Since(started)
		repository.observe(ctx, observability.Observation{Component: observability.ComponentControlPlane, Stage: observability.StageObjectCatalog,
			Result: observability.Result(facts.Result), ObjectCatalog: facts, Shardability: &shardability})
	}()
	if err := repository.writeObjectCatalog(ctx, catalog, facts); err != nil {
		return fmt.Errorf("alarmd controlplane: write object catalog: %w", err)
	}
	facts.Result = "success"
	return nil
}

func (repository *RedisCatalogRepository) writeObjectCatalog(ctx context.Context, catalog Catalog, facts *observability.ObjectCatalogFacts) error {
	content, err := buildObjectCatalogContent(catalog)
	if err != nil {
		return err
	}
	facts.ManifestBytes = len(content.manifestPayload)
	// What the published bytes say, reported whether it is any or none. This
	// is the first hop of the no-data count: the leader's own Catalog gauge
	// says how many Plans it compiled, and this says how many of them survive
	// into what every other process will read.
	repository.observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotRefreshed,
		Result: observability.ResultSuccess,
		Trace:  observability.TraceFields{SnapshotRevision: string(catalog.SnapshotRevision)},
		NoDataCensus: &observability.NoDataCensusFacts{
			Hop: observability.NoDataHopPublished, Plans: content.noDataPlans,
		},
	})
	_, knownObjects, knownContexts := repository.objectCatalog.snapshot()
	objectTTL := repository.objectTTL(catalog.ObjectRetention)

	// Digests the previous revision already proved present are skipped
	// outright; the rest are asked about in one EXISTS pipeline before any
	// payload is sent, so an unchanged publication costs one round trip per
	// batch and no bytes.
	objectKeys := make(map[string]execution.ObjectDigest, len(content.objects))
	contextKeys := make(map[string]execution.OutputContextDigest, len(content.contexts))
	candidates := make([]string, 0, len(content.objects)+len(content.contexts))
	for digest := range content.objects {
		key := repository.queryGroupObjectKey(digest)
		objectKeys[key] = digest
		if _, known := knownObjects[digest]; known {
			facts.Present++
			continue
		}
		candidates = append(candidates, key)
	}
	for digest := range content.contexts {
		key := repository.outputContextKey(digest)
		contextKeys[key] = digest
		if _, known := knownContexts[digest]; known {
			facts.Present++
			continue
		}
		candidates = append(candidates, key)
	}
	sort.Strings(candidates)
	missing := make([]string, 0, len(candidates))
	for start := 0; start < len(candidates); start += objectCatalogBatch {
		batch := candidates[start:minInt(start+objectCatalogBatch, len(candidates))]
		replies := make([]*redis.IntCmd, len(batch))
		if _, err := repository.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for index, key := range batch {
				replies[index] = pipe.Exists(ctx, key)
			}
			return nil
		}); err != nil {
			return fmt.Errorf("alarmd controlplane: probe catalog objects: %w", err)
		}
		for index, reply := range replies {
			if reply.Val() == 1 {
				facts.Present++
			} else {
				missing = append(missing, batch[index])
			}
		}
	}
	for start := 0; start < len(missing); start += objectCatalogBatch {
		batch := missing[start:minInt(start+objectCatalogBatch, len(missing))]
		replies := make([]*redis.BoolCmd, len(batch))
		if _, err := repository.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for index, key := range batch {
				var payload []byte
				if digest, ok := objectKeys[key]; ok {
					payload = content.objects[digest]
				} else {
					payload = content.contexts[contextKeys[key]]
				}
				replies[index] = pipe.SetNX(ctx, key, payload, objectTTL)
			}
			return nil
		}); err != nil {
			return fmt.Errorf("alarmd controlplane: write catalog objects: %w", err)
		}
		for index, reply := range replies {
			if reply.Val() {
				facts.Written++
				facts.ObjectBytes += len(content.objects[objectKeys[batch[index]]]) + len(content.contexts[contextKeys[batch[index]]])
			} else {
				// Another Control Leader stored the same digest first; the
				// bytes are the same by construction.
				facts.Present++
			}
		}
	}
	// The retention goes beside the manifest, with the manifest's own life:
	// the renewal reads it for the publication it renews, so a leader that
	// has just started keeps a long Plan's objects as long as the one that
	// published them did, without having admitted a Catalog first. Written
	// before the manifest, so no renewal can find the manifest without it:
	// a leader that stopped between the two would otherwise leave a
	// publication whose long Plans' objects are renewed for the catalog TTL.
	// It is named by revision, so a manifest collision after it is harmless.
	if objectTTL > repository.ttl {
		if err := repository.client.Set(ctx, repository.objectRetentionKey(catalog.SnapshotRevision),
			strconv.FormatInt(objectTTL.Milliseconds(), 10), repository.ttl).Err(); err != nil {
			return fmt.Errorf("alarmd controlplane: write catalog object retention: %w", err)
		}
	} else if err := repository.client.Del(ctx, repository.objectRetentionKey(catalog.SnapshotRevision)).Err(); err != nil {
		return fmt.Errorf("alarmd controlplane: clear catalog object retention: %w", err)
	}
	result, err := repository.client.Eval(ctx, writeCatalogManifestScript,
		[]string{repository.catalogManifestKey(catalog.SnapshotRevision)}, content.manifestPayload, repository.ttl.Milliseconds()).Int()
	if err != nil {
		return fmt.Errorf("alarmd controlplane: write catalog manifest: %w", err)
	}
	if result == -1 {
		return ErrCatalogManifestCollision
	}
	objects := make(map[execution.ObjectDigest]struct{}, len(content.objects))
	for digest := range content.objects {
		objects[digest] = struct{}{}
	}
	contexts := make(map[execution.OutputContextDigest]struct{}, len(content.contexts))
	for digest := range content.contexts {
		contexts[digest] = struct{}{}
	}
	repository.objectCatalog.replace(catalog.SnapshotRevision, objects, contexts)
	return nil
}

// LoadCatalogManifest reads the manifest of one publication.
func (repository *RedisCatalogRepository) LoadCatalogManifest(ctx context.Context, revision execution.SnapshotRevision) (CatalogManifest, error) {
	if repository == nil || repository.client == nil || revision == "" {
		return CatalogManifest{}, errors.New("alarmd controlplane: catalog manifest revision is required")
	}
	payload, err := repository.client.Get(ctx, repository.catalogManifestKey(revision)).Bytes()
	if errors.Is(err, redis.Nil) {
		return CatalogManifest{}, ErrCatalogManifestUnavailable
	}
	if err != nil {
		return CatalogManifest{}, activationDependencyIO(err)
	}
	var manifest CatalogManifest
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return CatalogManifest{}, &PersistedSnapshotCorruptError{Err: fmt.Errorf("decode catalog manifest: %w", err)}
	}
	if manifest.SchemaVersion != catalogManifestSchemaVersion || manifest.SnapshotRevision != revision {
		return CatalogManifest{}, &PersistedSnapshotCorruptError{Err: errors.New("catalog manifest does not match its revision")}
	}
	return manifest, nil
}

// renewObjectCatalog renews the manifest of the current publication and
// every object it names. A key the renewal cannot find is counted and makes
// the next write ask Redis about every digest again instead of trusting what
// this process remembers.
func (repository *RedisCatalogRepository) renewObjectCatalog(ctx context.Context, revision execution.SnapshotRevision) {
	if repository == nil || repository.client == nil || revision == "" {
		return
	}
	started := time.Now()
	facts := &observability.ObjectCatalogFacts{Operation: "renew", Result: "failure"}
	defer func() {
		facts.Duration = time.Since(started)
		repository.observe(ctx, observability.Observation{Component: observability.ComponentControlPlane, Stage: observability.StageObjectCatalog,
			Result: observability.Result(facts.Result), ObjectCatalog: facts})
	}()
	keys, err := repository.objectCatalogKeys(ctx, revision, facts)
	if err != nil {
		return
	}
	// A Query Group a cutover held back runs content the current manifest no
	// longer names; renewing only the manifest's objects would let what it
	// runs expire under it a catalog TTL later.
	keys = append(keys, repository.blockedObjectKeys(ctx)...)
	// Content is renewed for as long as the publication's longest Plan may
	// still read it once it is superseded; the manifest, like every other
	// catalog key, for the catalog TTL. The retention is read from beside
	// the manifest and not from memory, so the first renewal of a leader
	// that has just started does not cut a long Plan's objects back.
	objectTTL := repository.storedObjectRetention(ctx, revision)
	contentKeys := len(keys)
	keys = append(keys, repository.catalogManifestKey(revision))
	for start := 0; start < len(keys); start += objectCatalogBatch {
		batch := keys[start:minInt(start+objectCatalogBatch, len(keys))]
		replies := make([]*redis.BoolCmd, len(batch))
		if _, err := repository.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for index, key := range batch {
				ttl := objectTTL
				if start+index >= contentKeys {
					ttl = repository.ttl
				}
				replies[index] = pipe.PExpire(ctx, key, ttl)
			}
			if start+len(batch) == len(keys) && objectTTL > repository.ttl {
				// Not counted: absent is what a publication with no long Plan
				// has, and it is not a missing object.
				pipe.PExpire(ctx, repository.objectRetentionKey(revision), repository.ttl)
			}
			return nil
		}); err != nil {
			return
		}
		for _, reply := range replies {
			if reply.Val() {
				facts.Present++
			} else {
				facts.Missing++
			}
		}
	}
	if facts.Missing > 0 {
		repository.objectCatalog.forget()
	}
	facts.Result = "success"
}

// objectCatalogKeys lists the object keys of a revision from what this
// process remembers, or from the stored manifest when it remembers another
// revision. Reading the manifest makes the remembered set the manifest's:
// the renewal that follows proves each key present or drops the memory.
func (repository *RedisCatalogRepository) objectCatalogKeys(ctx context.Context, revision execution.SnapshotRevision, facts *observability.ObjectCatalogFacts) ([]string, error) {
	known, objects, contexts := repository.objectCatalog.snapshot()
	if known != revision {
		manifest, err := repository.LoadCatalogManifest(ctx, revision)
		if err != nil {
			return nil, err
		}
		objects = make(map[execution.ObjectDigest]struct{}, len(manifest.QueryGroups))
		for _, entry := range manifest.QueryGroups {
			objects[entry.ObjectDigest] = struct{}{}
		}
		contexts = make(map[execution.OutputContextDigest]struct{}, len(manifest.Plans))
		for _, entry := range manifest.Plans {
			contexts[entry.ContextDigest] = struct{}{}
		}
		repository.objectCatalog.replace(revision, objects, contexts)
	}
	facts.QueryGroups = len(objects)
	keys := make([]string, 0, len(objects)+len(contexts)+1)
	for digest := range objects {
		keys = append(keys, repository.queryGroupObjectKey(digest))
	}
	for digest := range contexts {
		keys = append(keys, repository.outputContextKey(digest))
	}
	sort.Strings(keys)
	return keys, nil
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

// noDataPlansInPayload counts the Plans a published Query Group object says
// detect no-data, by decoding the bytes.
//
// Decoded rather than counted off the struct the bytes came from: the struct is
// what the leader believes and the bytes are what every other process gets, and
// three releases were spent on the difference between the two being argued
// rather than measured.
func noDataPlansInPayload(payload []byte) (int, error) {
	var object QueryGroupObject
	if err := json.Unmarshal(payload, &object); err != nil {
		return 0, fmt.Errorf("alarmd controlplane: decode published Query Group object: %w", err)
	}
	count := 0
	for _, plan := range object.Plans {
		if plan.NoData != nil {
			count++
		}
	}
	return count, nil
}
