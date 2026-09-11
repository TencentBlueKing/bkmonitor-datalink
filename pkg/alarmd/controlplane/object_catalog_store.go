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
		payload, err := contract.CanonicalJSONV2(BuildQueryGroupObject(group))
		if err != nil {
			return objectCatalogContent{}, fmt.Errorf("alarmd controlplane: encode Query Group object: %w", err)
		}
		digest, err := contract.DeriveCanonicalDigestV2OverCanonical(queryGroupObjectContractVersion, payload)
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
	defer func() {
		facts.Duration = time.Since(started)
		repository.observe(ctx, observability.Observation{Component: observability.ComponentControlPlane, Stage: observability.StageObjectCatalog,
			Result: observability.Result(facts.Result), ObjectCatalog: facts})
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
	_, knownObjects, knownContexts := repository.objectCatalog.snapshot()

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
				replies[index] = pipe.SetNX(ctx, key, payload, repository.ttl)
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
		return CatalogManifest{}, fmt.Errorf("alarmd controlplane: decode catalog manifest: %w", err)
	}
	if manifest.SchemaVersion != catalogManifestSchemaVersion || manifest.SnapshotRevision != revision {
		return CatalogManifest{}, errors.New("alarmd controlplane: catalog manifest does not match its revision")
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
	keys = append(keys, repository.catalogManifestKey(revision))
	for start := 0; start < len(keys); start += objectCatalogBatch {
		batch := keys[start:minInt(start+objectCatalogBatch, len(keys))]
		replies := make([]*redis.BoolCmd, len(batch))
		if _, err := repository.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for index, key := range batch {
				replies[index] = pipe.PExpire(ctx, key, repository.ttl)
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
