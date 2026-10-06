// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The catalog index is the Control Leader's in-memory knowledge of what a
// publication contains: per Query Group its object digest and the
// identities of its Plans. Together with the persisted manifest, which
// names every Query Group's object and every Plan's output context, it
// answers everything an activation asks of a publication without reading
// the whole snapshot body: which Query Groups exist, which ones changed,
// and which output contexts each one references. Only the Query Groups
// the index does not know are read from the object catalog, so a Leader
// that published the catalog itself reads nothing, and a Leader that
// inherits an activation reads every object once and then only the
// changes of each later publication.
//
// Nothing about the index is persisted and nothing reads it back from
// Redis: it is derived from the catalog on publication or from the objects
// on demand, and a process that restarts derives it again. It is an
// accelerator, not an authority: while the snapshot body is still written,
// every activation audits the index against the body it loaded, and the
// audit's harmful direction (an entry kept whose content differs from the
// truth) must stay at zero before any reader may depend on the index.

// catalogIndexEntry is what the index keeps per Query Group.
type catalogIndexEntry struct {
	Group  execution.QueryGroupIdentity
	Digest execution.ObjectDigest
	// Plans are keyed by strategy and piece: the index is where a Query
	// Group's Plans are looked up against activation records, and two pieces
	// of one strategy are two records.
	Plans            []execution.PlanKey
	QueryRevision    execution.QueryRevision
	ScheduleRevision execution.ScheduleRevision
}

func (entry catalogIndexEntry) samePlans(plans []execution.PlanKey) bool {
	if len(entry.Plans) != len(plans) {
		return false
	}
	for index := range plans {
		if entry.Plans[index] != plans[index] {
			return false
		}
	}
	return true
}

type catalogIndex struct {
	mu       sync.Mutex
	revision execution.SnapshotRevision
	groups   map[execution.QueryGroupIdentity]catalogIndexEntry
}

func (index *catalogIndex) snapshot() (execution.SnapshotRevision, map[execution.QueryGroupIdentity]catalogIndexEntry) {
	index.mu.Lock()
	defer index.mu.Unlock()
	return index.revision, index.groups
}

func (index *catalogIndex) replace(revision execution.SnapshotRevision, groups map[execution.QueryGroupIdentity]catalogIndexEntry) {
	index.mu.Lock()
	defer index.mu.Unlock()
	index.revision, index.groups = revision, groups
}

// planIdentities lists a Query Group's Plans in catalog order, which is the
// order the object stores them in.
func planKeys(group QueryGroup) []execution.PlanKey {
	plans := make([]execution.PlanKey, 0, len(group.Plans))
	for _, plan := range group.Plans {
		plans = append(plans, plan.Key())
	}
	return plans
}

// indexFromCatalog derives the index entries of a catalog the Leader is
// about to publish, so the Leader that published a catalog never reads its
// objects back.
func indexFromCatalog(catalog Catalog) (map[execution.QueryGroupIdentity]catalogIndexEntry, error) {
	groups := make(map[execution.QueryGroupIdentity]catalogIndexEntry, len(catalog.QueryGroups))
	for _, group := range catalog.QueryGroups {
		if group.Identity == "" {
			return nil, errors.New("alarmd controlplane: catalog contains an empty Query Group")
		}
		if _, duplicate := groups[group.Identity]; duplicate {
			return nil, errors.New("alarmd controlplane: catalog contains a duplicate Query Group")
		}
		digest, err := DeriveQueryGroupObjectDigest(group)
		if err != nil {
			return nil, err
		}
		groups[group.Identity] = catalogIndexEntry{Group: group.Identity, Digest: digest, Plans: planKeys(group), QueryRevision: group.QueryPlan.QueryRevision, ScheduleRevision: group.ScheduleRevision}
	}
	return groups, nil
}

// ContentEntry is what a publication says about one Query Group without
// its content: the object digest, the Plans it carries, and the output
// context each Plan references, in the order the cutover compares them.
type ContentEntry struct {
	Digest execution.ObjectDigest
	Plans  []execution.PlanKey
	Refs   []execution.OutputContextRef
}

// PublishedContent is a publication seen through the manifest and the
// catalog index: every Query Group with its content identity, none with its
// content. Query Groups whose content a caller needs are loaded from the
// object catalog with LoadContentQueryGroups.
type PublishedContent struct {
	Publication SnapshotPublicationRef
	Groups      map[execution.QueryGroupIdentity]ContentEntry
}

// catalogIndexBatch bounds one pipelined read of Query Group objects during
// a cold load of the index.
const catalogIndexBatch = 500

// publishedContentMemo remembers the content of the publication read last:
// an activation asks for the same publication many times in one round (the
// reconcile, the cutover, every schedule it materializes), and a
// publication's content never changes once written.
type publishedContentMemo struct {
	mu          sync.Mutex
	publication SnapshotPublicationRef
	content     PublishedContent
}

func (memo *publishedContentMemo) lookup(publication SnapshotPublicationRef) (PublishedContent, bool) {
	memo.mu.Lock()
	defer memo.mu.Unlock()
	if memo.publication != publication || memo.publication == (SnapshotPublicationRef{}) {
		return PublishedContent{}, false
	}
	return memo.content, true
}

func (memo *publishedContentMemo) store(content PublishedContent) {
	memo.mu.Lock()
	defer memo.mu.Unlock()
	memo.publication, memo.content = content.Publication, content
}

// LoadPublishedContent describes a publication from its manifest and the
// catalog index, reading from the object catalog only the Query Groups the
// index does not know for this revision. A missing manifest reads as an
// unavailable snapshot, so callers keep the error they had when they read
// the body. The content read last is served from memory.
func (repository *RedisCatalogRepository) LoadPublishedContent(
	ctx context.Context,
	publication SnapshotPublicationRef,
) (PublishedContent, error) {
	if repository == nil || repository.client == nil {
		return PublishedContent{}, errors.New("alarmd controlplane: Redis catalog repository is required")
	}
	if publication.validate() != nil {
		return PublishedContent{}, errors.New("alarmd controlplane: complete publication is required")
	}
	if content, ok := repository.contentMemo.lookup(publication); ok {
		// The remembered content is immutable, but it stands only while the
		// manifest it came from is still there and the publication still
		// occurs; both are probed on every read, as the body read did.
		present, err := repository.client.Exists(ctx, repository.catalogManifestKey(publication.SnapshotRevision)).Result()
		if err != nil {
			return PublishedContent{}, activationDependencyIO(err)
		}
		if present == 1 {
			if err := repository.validateMemoizedPublication(ctx, publication); err != nil {
				return PublishedContent{}, err
			}
			return content, nil
		}
	}
	manifest, err := repository.LoadCatalogManifest(ctx, publication.SnapshotRevision)
	if errors.Is(err, ErrCatalogManifestUnavailable) {
		return PublishedContent{}, ErrSnapshotUnavailable
	}
	if err != nil {
		return PublishedContent{}, err
	}
	epochText, err := repository.client.Get(ctx, repository.epochForRevisionKey(publication.SnapshotRevision)).Result()
	if errors.Is(err, redis.Nil) {
		return PublishedContent{}, ErrSnapshotUnavailable
	}
	if err != nil {
		return PublishedContent{}, activationDependencyIO(err)
	}
	epoch, err := strconv.ParseUint(epochText, 10, 64)
	if err != nil || epoch == 0 {
		return PublishedContent{}, &PersistedSnapshotCorruptError{Err: errors.New("invalid publication epoch")}
	}
	if err := repository.validatePublicationOccurrence(ctx, publication,
		SnapshotPublicationRef{SnapshotRevision: publication.SnapshotRevision, PublicationEpoch: epoch}); err != nil {
		return PublishedContent{}, err
	}
	indexed, err := repository.ensureCatalogIndex(ctx, manifest)
	if err != nil {
		return PublishedContent{}, err
	}
	content, err := contentFromManifest(publication, manifest, indexed.entries)
	if err != nil {
		return PublishedContent{}, err
	}
	repository.contentMemo.store(content)
	return content, nil
}

// validateMemoizedPublication re-checks the epoch mapping and the
// publication occurrence of a remembered publication.
func (repository *RedisCatalogRepository) validateMemoizedPublication(ctx context.Context, publication SnapshotPublicationRef) error {
	epochText, err := repository.client.Get(ctx, repository.epochForRevisionKey(publication.SnapshotRevision)).Result()
	if errors.Is(err, redis.Nil) {
		return ErrSnapshotUnavailable
	}
	if err != nil {
		return activationDependencyIO(err)
	}
	epoch, err := strconv.ParseUint(epochText, 10, 64)
	if err != nil || epoch == 0 {
		return &PersistedSnapshotCorruptError{Err: errors.New("invalid publication epoch")}
	}
	return repository.validatePublicationOccurrence(ctx, publication,
		SnapshotPublicationRef{SnapshotRevision: publication.SnapshotRevision, PublicationEpoch: epoch})
}

func contentFromManifest(
	publication SnapshotPublicationRef,
	manifest CatalogManifest,
	entries map[execution.QueryGroupIdentity]catalogIndexEntry,
) (PublishedContent, error) {
	contexts := make(map[execution.PlanIdentity]execution.OutputContextDigest, len(manifest.Plans))
	for _, plan := range manifest.Plans {
		contexts[plan.Plan] = plan.ContextDigest
	}
	content := PublishedContent{Publication: publication, Groups: make(map[execution.QueryGroupIdentity]ContentEntry, len(manifest.QueryGroups))}
	for _, entry := range manifest.QueryGroups {
		indexed := entries[entry.QueryGroup]
		// The output context is the strategy's, shared by every piece of it,
		// so the refs are one per strategy however many pieces the group
		// holds - and a group holds at most one piece of a strategy anyway.
		refs := make([]execution.OutputContextRef, 0, len(indexed.Plans))
		referenced := make(map[execution.PlanIdentity]struct{}, len(indexed.Plans))
		for _, plan := range indexed.Plans {
			if _, done := referenced[plan.PlanIdentity]; done {
				continue
			}
			referenced[plan.PlanIdentity] = struct{}{}
			digest, ok := contexts[plan.PlanIdentity]
			if !ok || digest == "" {
				return PublishedContent{}, fmt.Errorf("alarmd controlplane: catalog manifest names no output context for Plan %s", plan.StrategyID)
			}
			refs = append(refs, execution.OutputContextRef{Plan: plan.PlanIdentity, Digest: digest})
		}
		sort.Slice(refs, func(i, j int) bool { return lessPlanIdentity(refs[i].Plan, refs[j].Plan) })
		content.Groups[entry.QueryGroup] = ContentEntry{Digest: entry.ObjectDigest, Plans: append([]execution.PlanKey(nil), indexed.Plans...), Refs: refs}
	}
	return content, nil
}

// ensuredCatalogIndex is one pass of ensureCatalogIndex: the entries now in
// force, which of them were read from the object catalog in this pass, and
// the entries the index held before it for the same Query Groups.
type ensuredCatalogIndex struct {
	entries  map[execution.QueryGroupIdentity]catalogIndexEntry
	reread   map[execution.QueryGroupIdentity]struct{}
	previous map[execution.QueryGroupIdentity]catalogIndexEntry
}

// ensureCatalogIndex makes the index cover every Query Group of the
// manifest at the manifest's digest, reading from the object catalog only
// the ones it does not know at that digest, and moves the index to the
// manifest's revision. Reused entries count as index hits, read ones as
// misses.
func (repository *RedisCatalogRepository) ensureCatalogIndex(
	ctx context.Context,
	manifest CatalogManifest,
) (ensuredCatalogIndex, error) {
	_, known := repository.catalogIndex.snapshot()
	ensured := ensuredCatalogIndex{
		entries:  make(map[execution.QueryGroupIdentity]catalogIndexEntry, len(manifest.QueryGroups)),
		reread:   make(map[execution.QueryGroupIdentity]struct{}),
		previous: make(map[execution.QueryGroupIdentity]catalogIndexEntry),
	}
	missing := make([]ManifestQueryGroup, 0)
	for _, entry := range manifest.QueryGroups {
		if entry.QueryGroup == "" || entry.ObjectDigest == "" {
			return ensuredCatalogIndex{}, errors.New("alarmd controlplane: catalog manifest names a Query Group without content")
		}
		if _, duplicate := ensured.entries[entry.QueryGroup]; duplicate {
			return ensuredCatalogIndex{}, errors.New("alarmd controlplane: catalog manifest names a Query Group twice")
		}
		indexed, ok := known[entry.QueryGroup]
		if ok {
			ensured.previous[entry.QueryGroup] = indexed
		}
		if ok && indexed.Digest == entry.ObjectDigest {
			ensured.entries[entry.QueryGroup] = indexed
			repository.controlReads.index.hits.Add(1)
			continue
		}
		ensured.entries[entry.QueryGroup] = catalogIndexEntry{Digest: entry.ObjectDigest}
		ensured.reread[entry.QueryGroup] = struct{}{}
		missing = append(missing, entry)
	}
	for start := 0; start < len(missing); start += catalogIndexBatch {
		batch := missing[start:minInt(start+catalogIndexBatch, len(missing))]
		objects, err := repository.loadQueryGroupObjects(ctx, batch)
		if err != nil {
			return ensuredCatalogIndex{}, err
		}
		for _, entry := range batch {
			object := objects[entry.ObjectDigest]
			if object.Identity != entry.QueryGroup {
				return ensuredCatalogIndex{}, fmt.Errorf("alarmd controlplane: catalog object of %s belongs to another Query Group", entry.QueryGroup)
			}
			plans := make([]execution.PlanKey, 0, len(object.Plans))
			for _, plan := range object.Plans {
				plans = append(plans, plan.Key())
			}
			ensured.entries[entry.QueryGroup] = catalogIndexEntry{Group: object.Identity, Digest: entry.ObjectDigest, Plans: plans, QueryRevision: object.QueryPlan.QueryRevision, ScheduleRevision: object.ScheduleRevision}
			repository.controlReads.index.misses.Add(1)
		}
	}
	repository.catalogIndex.replace(manifest.SnapshotRevision, ensured.entries)
	return ensured, nil
}

// loadQueryGroupObjects reads one batch of Query Group objects in a single
// pipeline, verifying each payload against its content address the way a
// single read does, and files them in the object cache.
func (repository *RedisCatalogRepository) loadQueryGroupObjects(
	ctx context.Context,
	batch []ManifestQueryGroup,
) (map[execution.ObjectDigest]QueryGroupObject, error) {
	replies := make([]*redis.StringCmd, len(batch))
	if _, err := repository.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for index, entry := range batch {
			replies[index] = pipe.Get(ctx, repository.queryGroupObjectKey(entry.ObjectDigest))
		}
		return nil
	}); err != nil && !errors.Is(err, redis.Nil) {
		return nil, activationDependencyIO(err)
	}
	objects := make(map[execution.ObjectDigest]QueryGroupObject, len(batch))
	for index, entry := range batch {
		payload, err := replies[index].Bytes()
		if errors.Is(err, redis.Nil) {
			repository.observeObjectRead(ctx, objectReadKindQueryGroup, objectReadMissing)
			return nil, ErrCatalogObjectUnavailable
		}
		if err != nil {
			return nil, activationDependencyIO(err)
		}
		domain, err := queryGroupObjectDomain(payload)
		if errors.Is(err, ErrCatalogObjectContractNewer) {
			repository.observeObjectRead(ctx, objectReadKindQueryGroup, objectReadNewer)
			return nil, err
		}
		if err != nil {
			repository.observeObjectRead(ctx, objectReadKindQueryGroup, objectReadInvalid)
			return nil, fmt.Errorf("%w: %v", ErrCatalogObjectCorrupt, err)
		}
		hashed, err := contract.DeriveCanonicalDigestV2OverCanonical(domain, payload)
		if err != nil || hashed != string(entry.ObjectDigest) {
			repository.observeObjectRead(ctx, objectReadKindQueryGroup, objectReadInvalid)
			return nil, ErrCatalogObjectCorrupt
		}
		var object QueryGroupObject
		if err := json.Unmarshal(payload, &object); err != nil || !knownQueryGroupObjectVersion(object.ContractVersion) || object.Identity == "" {
			repository.observeObjectRead(ctx, objectReadKindQueryGroup, objectReadInvalid)
			return nil, fmt.Errorf("%w: not a Query Group object of this contract", ErrCatalogObjectCorrupt)
		}
		repository.observeObjectRead(ctx, objectReadKindQueryGroup, objectReadMiss)
		// Cached in the same shape the single-object path caches, because both
		// write this key. Storing a bare object here and a decorated one there
		// made the cache hold two types under one key, which the reader only
		// finds out about by panicking on whichever it did not expect.
		repository.objects().store(repository.queryGroupObjectKey(entry.ObjectDigest), storedQueryGroupObject{
			object: object, noDataOccurrences: noDataOccurrencesIn(payload),
		}, len(payload))
		objects[entry.ObjectDigest] = object
	}
	return objects, nil
}

// LoadContentQueryGroups assembles the named Query Groups of a publication
// from the object catalog: the Query Group object and the output context
// of each of its Plans. It is the read an activation makes for the Query
// Groups it must compile or open, and nothing else.
func (repository *RedisCatalogRepository) LoadContentQueryGroups(
	ctx context.Context,
	content PublishedContent,
	identities []execution.QueryGroupIdentity,
) (map[execution.QueryGroupIdentity]QueryGroup, error) {
	if repository == nil || repository.client == nil {
		return nil, errors.New("alarmd controlplane: Redis catalog repository is required")
	}
	groups := make(map[execution.QueryGroupIdentity]QueryGroup, len(identities))
	for _, identity := range identities {
		entry, ok := content.Groups[identity]
		if !ok {
			return nil, fmt.Errorf("%w: publication does not name Query Group %s", ErrCatalogObjectUnavailable, identity)
		}
		object, err := repository.LoadQueryGroupObject(ctx, entry.Digest)
		if err != nil {
			return nil, err
		}
		if object.Identity != identity {
			return nil, fmt.Errorf("alarmd controlplane: catalog object of %s belongs to another Query Group", identity)
		}
		contexts := make(map[execution.PlanIdentity]OutputContextObject, len(entry.Refs))
		for _, ref := range entry.Refs {
			context, err := repository.LoadOutputContext(ctx, ref.Digest)
			if err != nil {
				return nil, err
			}
			contexts[ref.Plan] = context
		}
		group, err := AssembleQueryGroup(object, contexts)
		if err != nil {
			return nil, err
		}
		groups[identity] = group
	}
	return groups, nil
}
