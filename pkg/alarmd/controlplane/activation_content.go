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

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// publishedGroups is how an activation sees a publication once it no longer
// loads the snapshot body: the Query Group map every activation helper
// takes, whose values carry only the identity and the Plan identities until
// a Query Group's content is materialized; the content entries the cutover
// compares (object digest and output context references); and the loader
// that fetches full Query Groups on demand from the object catalog and
// remembers them. Helpers that only need to know which Query Groups exist
// and which Plans they carry work on the map as is; the few that open or
// compile a Query Group ask for its content first, so an activation reads
// exactly the objects of the Query Groups it must act on.
type publishedGroups struct {
	content PublishedContent
	groups  map[execution.QueryGroupIdentity]QueryGroup
	full    map[execution.QueryGroupIdentity]struct{}
}

// loadPublishedGroups describes a publication through the manifest and the
// catalog index; no Query Group content is read.
func (repository *RedisCatalogRepository) loadPublishedGroups(
	ctx context.Context,
	publication SnapshotPublicationRef,
) (*publishedGroups, error) {
	content, err := repository.LoadPublishedContent(ctx, publication)
	if err != nil {
		return nil, err
	}
	published := &publishedGroups{content: content,
		groups: make(map[execution.QueryGroupIdentity]QueryGroup, len(content.Groups)),
		full:   make(map[execution.QueryGroupIdentity]struct{})}
	for identity, entry := range content.Groups {
		plans := make([]FrozenPlan, 0, len(entry.Plans))
		for _, plan := range entry.Plans {
			plans = append(plans, FrozenPlan{Identity: plan})
		}
		published.groups[identity] = QueryGroup{Identity: identity, Plans: plans}
	}
	return published, nil
}

// segmentContents is what the cutover compares per Query Group, taken from
// the publication's content entries rather than derived from bodies.
func (published *publishedGroups) segmentContents() map[execution.QueryGroupIdentity]segmentContent {
	contents := make(map[execution.QueryGroupIdentity]segmentContent, len(published.content.Groups))
	for identity, entry := range published.content.Groups {
		contents[identity] = segmentContent{digest: entry.Digest, refs: append([]execution.OutputContextRef(nil), entry.Refs...)}
	}
	return contents
}

// identities lists the publication's Query Groups in a fixed order.
func (published *publishedGroups) identities() []execution.QueryGroupIdentity {
	identities := make([]execution.QueryGroupIdentity, 0, len(published.groups))
	for identity := range published.groups {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i] < identities[j] })
	return identities
}

// loaded returns the materialized Query Groups among the given identities,
// in identity order; a Query Group that was not materialized is an error,
// because a caller that compiles or opens it would silently work on an
// identity-only shell.
func (published *publishedGroups) loaded(identities []execution.QueryGroupIdentity) ([]QueryGroup, error) {
	sorted := append([]execution.QueryGroupIdentity(nil), identities...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	groups := make([]QueryGroup, 0, len(sorted))
	for _, identity := range sorted {
		if _, ok := published.full[identity]; !ok {
			return nil, fmt.Errorf("alarmd controlplane: Query Group %s was not materialized before use", identity)
		}
		groups = append(groups, published.groups[identity])
	}
	return groups, nil
}

// materialize loads the content of the named Query Groups from the object
// catalog, in pipelined batches, and replaces their identity-only shells.
// Query Groups already materialized are not read again.
func (repository *RedisCatalogRepository) materialize(
	ctx context.Context,
	published *publishedGroups,
	identities []execution.QueryGroupIdentity,
) error {
	wanted := make([]ManifestQueryGroup, 0, len(identities))
	seen := make(map[execution.QueryGroupIdentity]struct{}, len(identities))
	for _, identity := range identities {
		if _, done := published.full[identity]; done {
			continue
		}
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		entry, ok := published.content.Groups[identity]
		if !ok {
			return fmt.Errorf("%w: publication does not name Query Group %s", ErrCatalogObjectUnavailable, identity)
		}
		seen[identity] = struct{}{}
		wanted = append(wanted, ManifestQueryGroup{QueryGroup: identity, ObjectDigest: entry.Digest})
	}
	sort.Slice(wanted, func(i, j int) bool { return wanted[i].QueryGroup < wanted[j].QueryGroup })
	for start := 0; start < len(wanted); start += catalogIndexBatch {
		batch := wanted[start:minInt(start+catalogIndexBatch, len(wanted))]
		objects, err := repository.loadQueryGroupObjectsCached(ctx, batch)
		if err != nil {
			return err
		}
		refs := make([]execution.OutputContextRef, 0, len(batch))
		for _, entry := range batch {
			refs = append(refs, published.content.Groups[entry.QueryGroup].Refs...)
		}
		contexts, err := repository.loadOutputContexts(ctx, refs)
		if err != nil {
			return err
		}
		for _, entry := range batch {
			object := objects[entry.ObjectDigest]
			if object.Identity != entry.QueryGroup {
				return fmt.Errorf("alarmd controlplane: catalog object of %s belongs to another Query Group", entry.QueryGroup)
			}
			byPlan := make(map[execution.PlanIdentity]OutputContextObject)
			for _, ref := range published.content.Groups[entry.QueryGroup].Refs {
				byPlan[ref.Plan] = contexts[ref.Digest]
			}
			group, err := AssembleQueryGroup(object, byPlan)
			if err != nil {
				return err
			}
			published.groups[entry.QueryGroup] = group
			published.full[entry.QueryGroup] = struct{}{}
		}
	}
	return nil
}

// loadQueryGroupObjectsCached serves what the object cache holds and reads
// the rest in one pipeline.
func (repository *RedisCatalogRepository) loadQueryGroupObjectsCached(
	ctx context.Context,
	batch []ManifestQueryGroup,
) (map[execution.ObjectDigest]QueryGroupObject, error) {
	objects := make(map[execution.ObjectDigest]QueryGroupObject, len(batch))
	missing := make([]ManifestQueryGroup, 0, len(batch))
	for _, entry := range batch {
		if value, ok := repository.objectCache.lookup(repository.queryGroupObjectKey(entry.ObjectDigest)); ok {
			if stored, ok := value.(storedQueryGroupObject); ok {
				repository.observeObjectRead(ctx, objectReadKindQueryGroup, objectReadHit)
				objects[entry.ObjectDigest] = stored.object
				continue
			}
		}
		missing = append(missing, entry)
	}
	if len(missing) == 0 {
		return objects, nil
	}
	read, err := repository.loadQueryGroupObjects(ctx, missing)
	if err != nil {
		return nil, err
	}
	for digest, object := range read {
		objects[digest] = object
	}
	return objects, nil
}

// loadOutputContexts reads the named output contexts, serving cached ones
// and reading the rest in one pipeline with the same verification a single
// read applies.
func (repository *RedisCatalogRepository) loadOutputContexts(
	ctx context.Context,
	refs []execution.OutputContextRef,
) (map[execution.OutputContextDigest]OutputContextObject, error) {
	contexts := make(map[execution.OutputContextDigest]OutputContextObject, len(refs))
	missing := make([]execution.OutputContextDigest, 0, len(refs))
	for _, ref := range refs {
		if _, done := contexts[ref.Digest]; done {
			continue
		}
		if value, ok := repository.objectCache.lookup(repository.outputContextKey(ref.Digest)); ok {
			if context, ok := value.(OutputContextObject); ok {
				repository.observeObjectRead(ctx, objectReadKindOutputContext, objectReadHit)
				contexts[ref.Digest] = context
				continue
			}
		}
		contexts[ref.Digest] = OutputContextObject{}
		missing = append(missing, ref.Digest)
	}
	for start := 0; start < len(missing); start += catalogIndexBatch {
		batch := missing[start:minInt(start+catalogIndexBatch, len(missing))]
		replies := make([]*redis.StringCmd, len(batch))
		if _, err := repository.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for index, digest := range batch {
				replies[index] = pipe.Get(ctx, repository.outputContextKey(digest))
			}
			return nil
		}); err != nil && !errors.Is(err, redis.Nil) {
			return nil, activationDependencyIO(err)
		}
		for index, digest := range batch {
			payload, err := replies[index].Bytes()
			if errors.Is(err, redis.Nil) {
				repository.observeObjectRead(ctx, objectReadKindOutputContext, objectReadMissing)
				return nil, ErrCatalogObjectUnavailable
			}
			if err != nil {
				return nil, activationDependencyIO(err)
			}
			hashed, err := contract.DeriveCanonicalDigestV2OverCanonical(outputContextContractVersion, payload)
			if err != nil || hashed != string(digest) {
				repository.observeObjectRead(ctx, objectReadKindOutputContext, objectReadInvalid)
				return nil, ErrCatalogObjectCorrupt
			}
			var context OutputContextObject
			if err := json.Unmarshal(payload, &context); err != nil || context.ContractVersion != outputContextContractVersion {
				repository.observeObjectRead(ctx, objectReadKindOutputContext, objectReadInvalid)
				return nil, fmt.Errorf("%w: not an output context of this contract", ErrCatalogObjectCorrupt)
			}
			repository.observeObjectRead(ctx, objectReadKindOutputContext, objectReadMiss)
			repository.objectCache.store(repository.outputContextKey(digest), context, len(payload))
			contexts[digest] = context
		}
	}
	return contexts, nil
}

// snapshotOf assembles the whole publication as a PublishedSnapshot from the
// object catalog, in identity order. It is the cold read a Leader makes
// once when it needs every Query Group's content, such as the source
// reconciler's last-good catalog after a restart.
func (repository *RedisCatalogRepository) snapshotOf(
	ctx context.Context,
	published *publishedGroups,
) (PublishedSnapshot, error) {
	identities := published.identities()
	if err := repository.materialize(ctx, published, identities); err != nil {
		return PublishedSnapshot{}, err
	}
	groups, err := published.loaded(identities)
	if err != nil {
		return PublishedSnapshot{}, err
	}
	return PublishedSnapshot{SchemaVersion: snapshotSchemaVersion, Publication: published.content.Publication, QueryGroups: groups}, nil
}

// compilePublishedGroups compiles the given Query Groups into activation
// records and initial schedule segments for a publication. It is the
// compile of the body path, applied to only the Query Groups an activation
// must compile: content is a pure function of itself, so Query Groups whose
// content did not change keep the records of the activation before.
func compilePublishedGroups(
	ctx context.Context,
	compiler RuntimePlanCompiler,
	stateSemantics strategy.StateSemantics,
	publication SnapshotPublicationRef,
	groups []QueryGroup,
	named map[execution.QueryGroupIdentity]ContentEntry,
	boundary execution.EvaluationTime,
) ([]PlanActivationRecord, []execution.ScheduleSegmentFact, error) {
	return compilePublishedActivation(ctx, compiler, stateSemantics,
		PublishedSnapshot{SchemaVersion: snapshotSchemaVersion, Publication: publication, QueryGroups: groups},
		named, boundary)
}

// carriedActivationRecords picks, for an activation of next over previous,
// the Query Groups whose records can be carried from previous unchanged and
// the ones that must be compiled from their content. A Query Group is
// carried when its object digest and its output context references are the
// ones the previous activation acted on, every one of its Plans has a
// record in previous, and it is neither returning from retirement nor
// being reactivated from a hold; everything else is compiled.
//
// The last two exclusions do not decide anything today: a returning or a
// reactivating Query Group was not part of the previous activation, so
// previousContent knows no digest for it and it is compiled on that alone
// (a test pins the behaviour, not this guard). They stay for the day
// previousContent learns about Draining Query Groups; until then a change
// to either is a change nothing can observe.
func carriedActivationRecords(
	published *publishedGroups,
	previous ActivationState,
	previousContent activatedContent,
	returning map[execution.QueryGroupIdentity]struct{},
	reactivating map[execution.QueryGroupIdentity]struct{},
) (carried []PlanActivationRecord, compile []execution.QueryGroupIdentity, err error) {
	records, err := activationRecordMap(previous.Plans)
	if err != nil {
		return nil, nil, err
	}
	for _, identity := range published.identities() {
		entry := published.content.Groups[identity]
		group := published.groups[identity]
		_, isReturning := returning[identity]
		_, isReactivating := reactivating[identity]
		previousDigest, known := previousContent.digests[identity]
		previousRefs, refsKnown := previousContent.refsFor(group)
		keep := known && !isReturning && !isReactivating && previousDigest == entry.Digest && refsKnown &&
			execution.SameOutputContextRefs(previousRefs, entry.Refs)
		if keep {
			for _, plan := range entry.Plans {
				if _, ok := records[plan]; !ok {
					keep = false
					break
				}
			}
		}
		if !keep {
			compile = append(compile, identity)
			continue
		}
		for _, plan := range entry.Plans {
			carried = append(carried, records[plan])
		}
	}
	return carried, compile, nil
}
