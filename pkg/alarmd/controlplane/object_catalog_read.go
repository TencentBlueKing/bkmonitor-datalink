// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Worker that reads a Query Group by content reads a few kilobytes named
// by a digest instead of the whole Snapshot named by a revision. The object
// is immutable, so a copy in this process never goes stale and is evicted
// only for room; two Slots of one process that miss the same digest share
// one network read; and the bytes read are hashed before they are trusted.
//
// Every Segment written before the catalog existed, and every Segment whose
// object is not stored anymore, is read the way it always was, from the
// Snapshot the Segment's Publication names, and that fallback is counted:
// while the Snapshot is still written it is the authority, and a fallback
// that nobody sees is how a catalog silently stops being used.

// ErrCatalogObjectCorrupt reports stored bytes that do not hash to the
// digest that names them.
var ErrCatalogObjectCorrupt = errors.New("alarmd controlplane: catalog object does not match its digest")

const (
	objectReadKindQueryGroup    = "query_group"
	objectReadKindOutputContext = "output_context"
	objectReadKindSegment       = "segment"

	objectReadHit     = "hit"
	objectReadMiss    = "miss"
	objectReadShare   = "share"
	objectReadMissing = "missing"
	objectReadInvalid = "invalid"

	segmentReadObject         = "object"
	segmentReadLegacySegment  = "legacy_segment"
	segmentReadWithoutRef     = "segment_without_ref"
	segmentReadObjectMissing  = "object_missing"
	segmentReadObjectInvalid  = "object_invalid"
	segmentReadObjectMismatch = "object_mismatch"
)

type objectCacheEntry struct {
	key   string
	bytes int
	value any
}

// objectReadCache keeps decoded catalog objects by digest, least recently
// used first out, bounded by entries and by the bytes of the payloads they
// were decoded from.
type objectReadCache struct {
	mu         sync.Mutex
	entries    map[string]*list.Element
	order      *list.List
	bytes      int
	maxEntries int
	maxBytes   int
}

func newObjectReadCache(maxEntries, maxBytes int) *objectReadCache {
	return &objectReadCache{entries: make(map[string]*list.Element), order: list.New(), maxEntries: maxEntries, maxBytes: maxBytes}
}

func (cache *objectReadCache) lookup(key string) (any, bool) {
	if cache == nil {
		return nil, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	element, ok := cache.entries[key]
	if !ok {
		return nil, false
	}
	cache.order.MoveToFront(element)
	return element.Value.(*objectCacheEntry).value, true
}

func (cache *objectReadCache) store(key string, value any, bytes int) {
	if cache == nil || cache.maxEntries <= 0 || cache.maxBytes <= 0 || bytes > cache.maxBytes {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if element, ok := cache.entries[key]; ok {
		cache.order.MoveToFront(element)
		return
	}
	for cache.order.Len() > 0 && (cache.order.Len() >= cache.maxEntries || cache.bytes+bytes > cache.maxBytes) {
		oldest := cache.order.Back()
		entry := oldest.Value.(*objectCacheEntry)
		cache.order.Remove(oldest)
		delete(cache.entries, entry.key)
		cache.bytes -= entry.bytes
	}
	cache.entries[key] = cache.order.PushFront(&objectCacheEntry{key: key, bytes: bytes, value: value})
	cache.bytes += bytes
}

type objectReadFlight struct {
	done  chan struct{}
	value any
	bytes int
	err   error
}

// objectReadFlights joins concurrent reads of one digest in this process
// into one network read, the way Snapshot reads are joined.
type objectReadFlights struct {
	mu    sync.Mutex
	byKey map[string]*objectReadFlight
}

// ConfigureObjectCache bounds the decoded catalog objects this process keeps.
func (repository *RedisCatalogRepository) ConfigureObjectCache(maxEntries, maxBytes int) error {
	if repository == nil || maxEntries <= 0 || maxBytes <= 0 {
		return errors.New("alarmd controlplane: invalid catalog object cache budget")
	}
	repository.objectCache = newObjectReadCache(maxEntries, maxBytes)
	return nil
}

func (repository *RedisCatalogRepository) observeObjectRead(ctx context.Context, kind, result string) {
	repository.observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageObjectRead,
		Result: observability.ResultSuccess, ObjectRead: &observability.ObjectReadFacts{Kind: kind, Result: result},
	})
}

// loadObject reads one catalog object by key: the process cache first, then
// one shared network read whose bytes are hashed against digest before they
// are decoded, cached and returned.
func (repository *RedisCatalogRepository) loadObject(
	ctx context.Context,
	kind, key, domain, digest string,
	decode func([]byte) (any, error),
) (any, error) {
	if repository == nil || repository.client == nil || digest == "" {
		return nil, errors.New("alarmd controlplane: catalog object digest is required")
	}
	if value, ok := repository.objectCache.lookup(key); ok {
		repository.observeObjectRead(ctx, kind, objectReadHit)
		return value, nil
	}
	flights := &repository.objectFlights
	flights.mu.Lock()
	if flights.byKey == nil {
		flights.byKey = make(map[string]*objectReadFlight)
	}
	flight, joined := flights.byKey[key]
	if !joined {
		flight = &objectReadFlight{done: make(chan struct{})}
		flights.byKey[key] = flight
	}
	flights.mu.Unlock()
	if joined {
		select {
		case <-flight.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if flight.err == nil {
			repository.observeObjectRead(ctx, kind, objectReadShare)
		}
		return flight.value, flight.err
	}
	flight.value, flight.bytes, flight.err = repository.readObject(ctx, kind, key, domain, digest, decode)
	flights.mu.Lock()
	delete(flights.byKey, key)
	flights.mu.Unlock()
	close(flight.done)
	if flight.err == nil {
		repository.objectCache.store(key, flight.value, flight.bytes)
	}
	return flight.value, flight.err
}

func (repository *RedisCatalogRepository) readObject(
	ctx context.Context,
	kind, key, domain, digest string,
	decode func([]byte) (any, error),
) (any, int, error) {
	payload, err := repository.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		repository.observeObjectRead(ctx, kind, objectReadMissing)
		return nil, 0, ErrCatalogObjectUnavailable
	}
	if err != nil {
		return nil, 0, activationDependencyIO(err)
	}
	hashed, err := contract.DeriveCanonicalDigestV2OverCanonical(domain, payload)
	if err != nil || hashed != digest {
		repository.observeObjectRead(ctx, kind, objectReadInvalid)
		return nil, 0, ErrCatalogObjectCorrupt
	}
	value, err := decode(payload)
	if err != nil {
		repository.observeObjectRead(ctx, kind, objectReadInvalid)
		return nil, 0, fmt.Errorf("%w: %v", ErrCatalogObjectCorrupt, err)
	}
	repository.observeObjectRead(ctx, kind, objectReadMiss)
	return value, len(payload), nil
}

// LoadQueryGroupObject reads the execution content stored under digest.
func (repository *RedisCatalogRepository) LoadQueryGroupObject(ctx context.Context, digest execution.ObjectDigest) (QueryGroupObject, error) {
	value, err := repository.loadObject(ctx, objectReadKindQueryGroup, repository.queryGroupObjectKey(digest), queryGroupObjectContractVersion, string(digest),
		func(payload []byte) (any, error) {
			var object QueryGroupObject
			if err := json.Unmarshal(payload, &object); err != nil {
				return nil, err
			}
			if object.ContractVersion != queryGroupObjectContractVersion || object.Identity == "" {
				return nil, errors.New("not a Query Group object of this contract")
			}
			return object, nil
		})
	if err != nil {
		return QueryGroupObject{}, err
	}
	return value.(QueryGroupObject), nil
}

// LoadOutputContext reads the rendering context stored under digest.
func (repository *RedisCatalogRepository) LoadOutputContext(ctx context.Context, digest execution.OutputContextDigest) (OutputContextObject, error) {
	value, err := repository.loadObject(ctx, objectReadKindOutputContext, repository.outputContextKey(digest), outputContextContractVersion, string(digest),
		func(payload []byte) (any, error) {
			var object OutputContextObject
			if err := json.Unmarshal(payload, &object); err != nil {
				return nil, err
			}
			if object.ContractVersion != outputContextContractVersion {
				return nil, errors.New("not an output context of this contract")
			}
			return object, nil
		})
	if err != nil {
		return OutputContextObject{}, err
	}
	return value.(OutputContextObject), nil
}

// AssembleQueryGroup is the inverse of BuildQueryGroupObject and
// BuildOutputContext: it puts one execution object and the output context of
// each of its Plans back together into the QueryGroup the rest of the module
// reads. PlanRevision is left empty; nothing reads it.
func AssembleQueryGroup(object QueryGroupObject, contexts map[execution.PlanIdentity]OutputContextObject) (QueryGroup, error) {
	if object.ContractVersion != queryGroupObjectContractVersion || object.Identity == "" {
		return QueryGroup{}, errors.New("alarmd controlplane: not a Query Group object of this contract")
	}
	group := QueryGroup{Identity: object.Identity, QueryPlan: object.QueryPlan, MembershipDigest: object.MembershipDigest,
		ScheduleRevision: object.ScheduleRevision, Plans: make([]FrozenPlan, 0, len(object.Plans))}
	for _, plan := range object.Plans {
		context, ok := contexts[plan.Identity]
		if !ok {
			return QueryGroup{}, fmt.Errorf("alarmd controlplane: no output context for Plan %s", plan.Identity.StrategyID)
		}
		if context.ContractVersion != outputContextContractVersion || context.Identity != plan.Identity ||
			context.StrategyRef.TenantID != plan.Strategy.TenantID || context.StrategyRef.StrategyID != plan.Strategy.StrategyID {
			return QueryGroup{}, fmt.Errorf("alarmd controlplane: output context does not belong to Plan %s", plan.Identity.StrategyID)
		}
		strategyIR := plan.StrategyIR
		strategyIR.StrategyRef = context.StrategyRef
		group.Plans = append(group.Plans, FrozenPlan{
			Identity: plan.Identity,
			Plan: contract.EvaluationPlanV2{
				PlanID: plan.PlanID, StrategyRef: context.StrategyRef, InputProjection: plan.InputProjection,
				SourceCompatibility: context.SourceCompatibility, OutputIdentity: plan.OutputIdentity,
				SubjectFacts: context.SubjectFacts, LegacyOutput: context.LegacyOutput, TargetScope: plan.TargetScope,
				StrategyIR: strategyIR, WireFormat: context.WireFormat, TerminalReasonCode: plan.TerminalReasonCode,
			},
			StateGeneration: plan.StateGeneration, ScheduleSpec: plan.ScheduleSpec, ScheduleRevision: plan.ScheduleRevision,
			RequirementTemplates: plan.RequirementTemplates, QueryPlans: plan.QueryPlans,
		})
	}
	return group, nil
}

// LoadSegmentQueryGroup reads the Query Group a Segment was activated with:
// by content when the Segment names it and the content is stored, and from
// the Snapshot the Segment's Publication names otherwise. Every way the
// content path is not taken is counted under its own reason.
func (repository *RedisCatalogRepository) LoadSegmentQueryGroup(
	ctx context.Context,
	segment execution.ScheduleSegmentFact,
	fallback func(context.Context) (QueryGroup, error),
) (QueryGroup, error) {
	if repository == nil || fallback == nil {
		return QueryGroup{}, errors.New("alarmd controlplane: Segment Query Group read requires a fallback")
	}
	if segment.ObjectDigest == "" {
		repository.observeObjectRead(ctx, objectReadKindSegment, segmentReadLegacySegment)
		return fallback(ctx)
	}
	group, result, err := repository.loadSegmentQueryGroupByContent(ctx, segment)
	if err == nil {
		repository.observeObjectRead(ctx, objectReadKindSegment, segmentReadObject)
		return group, nil
	}
	if result == "" {
		return QueryGroup{}, err
	}
	repository.observeObjectRead(ctx, objectReadKindSegment, result)
	return fallback(ctx)
}

// loadSegmentQueryGroupByContent returns the assembled Query Group, or the
// reason the content path cannot serve this Segment. An empty reason with an
// error is an I/O failure the caller must return rather than work around.
func (repository *RedisCatalogRepository) loadSegmentQueryGroupByContent(ctx context.Context, segment execution.ScheduleSegmentFact) (QueryGroup, string, error) {
	object, err := repository.LoadQueryGroupObject(ctx, segment.ObjectDigest)
	switch {
	case errors.Is(err, ErrCatalogObjectUnavailable):
		return QueryGroup{}, segmentReadObjectMissing, err
	case errors.Is(err, ErrCatalogObjectCorrupt):
		return QueryGroup{}, segmentReadObjectInvalid, err
	case err != nil:
		return QueryGroup{}, "", err
	}
	if object.Identity != segment.QueryGroup {
		return QueryGroup{}, segmentReadObjectMismatch, errors.New("alarmd controlplane: catalog object belongs to another Query Group")
	}
	contexts := make(map[execution.PlanIdentity]OutputContextObject, len(object.Plans))
	for _, plan := range object.Plans {
		ref := segment.OutputContextRefFor(plan.Identity)
		if ref == "" {
			return QueryGroup{}, segmentReadWithoutRef, errors.New("alarmd controlplane: Segment names no output context for a Plan")
		}
		context, err := repository.LoadOutputContext(ctx, ref)
		switch {
		case errors.Is(err, ErrCatalogObjectUnavailable):
			return QueryGroup{}, segmentReadObjectMissing, err
		case errors.Is(err, ErrCatalogObjectCorrupt):
			return QueryGroup{}, segmentReadObjectInvalid, err
		case err != nil:
			return QueryGroup{}, "", err
		}
		contexts[plan.Identity] = context
	}
	group, err := AssembleQueryGroup(object, contexts)
	if err != nil {
		return QueryGroup{}, segmentReadObjectMismatch, err
	}
	return group, "", nil
}
