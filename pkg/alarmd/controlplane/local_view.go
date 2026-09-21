// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"context"
	"errors"
	"sync"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The local view is what this Worker holds of the control plane's content
// for the Query Groups it owns: for each, the execution object and the
// output contexts its last Slot was frozen from. decision-016 sizes the view
// stream, the reconnect burst and the per-Worker send buffer by that number,
// and until now it could only be computed off Redis by a script run by hand.
// Here the Worker reports it itself, from the bytes it actually read.
//
// The view is kept per Query Group and summed over the owned set at read
// time, so a Query Group the Worker released drops out of the sum the moment
// it is released and not when its entry happens to be evicted from the
// object cache; the cache is bounded by memory, the view by ownership, and
// the two are different numbers. A Query Group the Worker owns but has not
// frozen a Slot for yet, or whose last Slot was served from the Snapshot
// rather than by content, is not in the view: read local_view_query_groups
// against worker_owned_query_groups for that difference.

// LocalView is the size of this Worker's view over one owned set.
type LocalView struct {
	// QueryGroups is how many of the owned Query Groups have an object in
	// the view.
	QueryGroups int
	// ObjectBytes is the stored bytes of their execution objects, and
	// OutputContextBytes of the output contexts those objects' Plans name,
	// each context counted once per Query Group that names it.
	ObjectBytes, OutputContextBytes int
	// Plans is how many Plans those objects carry.
	Plans int
}

// localViewEntry is what one Query Group's last content read cost in bytes.
type localViewEntry struct {
	object             execution.ObjectDigest
	objectBytes        int
	outputContextBytes int
	plans              int
}

type localView struct {
	mu      sync.Mutex
	byGroup map[execution.QueryGroupIdentity]localViewEntry
}

func (view *localView) record(queryGroup execution.QueryGroupIdentity, entry localViewEntry) {
	view.mu.Lock()
	defer view.mu.Unlock()
	if view.byGroup == nil {
		view.byGroup = make(map[execution.QueryGroupIdentity]localViewEntry)
	}
	view.byGroup[queryGroup] = entry
}

func (view *localView) forget(queryGroup execution.QueryGroupIdentity) {
	view.mu.Lock()
	defer view.mu.Unlock()
	delete(view.byGroup, queryGroup)
}

// LocalView sums the view over owned and drops what it holds for any Query
// Group outside it. It is meant to be read at scrape time with the owned set
// the Worker reports beside it.
func (repository *RedisCatalogRepository) LocalView(owned []execution.QueryGroupIdentity) LocalView {
	summed := LocalView{}
	if repository == nil {
		return summed
	}
	view := &repository.localView
	view.mu.Lock()
	defer view.mu.Unlock()
	kept := make(map[execution.QueryGroupIdentity]struct{}, len(owned))
	for _, queryGroup := range owned {
		kept[queryGroup] = struct{}{}
		entry, ok := view.byGroup[queryGroup]
		if !ok {
			continue
		}
		summed.QueryGroups++
		summed.ObjectBytes += entry.objectBytes
		summed.OutputContextBytes += entry.outputContextBytes
		summed.Plans += entry.plans
	}
	for queryGroup := range view.byGroup {
		if _, keep := kept[queryGroup]; !keep {
			delete(view.byGroup, queryGroup)
		}
	}
	return summed
}

// MissingObjects is how many of the named objects this Worker cannot read:
// not in its cache and not stored under the catalog prefix. Asked by the
// view stream's client once per install, for the objects the install
// brought, so a Worker can say of a view it installed whether the content
// it names is there to execute -- a fact of the view reported with the
// receipt, never invented as present. The store is asked in one pipeline of
// EXISTS for the objects the cache does not hold.
func (repository *RedisCatalogRepository) MissingObjects(
	ctx context.Context,
	objects []execution.ObjectDigest,
	contexts []execution.OutputContextDigest,
) (int, error) {
	if repository == nil || repository.client == nil {
		return 0, errors.New("alarmd controlplane: Redis catalog repository is required")
	}
	keys := make([]string, 0, len(objects)+len(contexts))
	seen := make(map[string]struct{}, cap(keys))
	consider := func(key string) {
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		if _, _, cached := repository.objectCache.lookup(key); cached {
			return
		}
		keys = append(keys, key)
	}
	for _, digest := range objects {
		if digest != "" {
			consider(repository.queryGroupObjectKey(digest))
		}
	}
	for _, digest := range contexts {
		if digest != "" {
			consider(repository.outputContextKey(digest))
		}
	}
	missing := 0
	for start := 0; start < len(keys); start += objectCatalogBatch {
		batch := keys[start:minInt(start+objectCatalogBatch, len(keys))]
		replies := make([]*redis.IntCmd, len(batch))
		if _, err := repository.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for index, key := range batch {
				replies[index] = pipe.Exists(ctx, key)
			}
			return nil
		}); err != nil {
			return 0, activationDependencyIO(err)
		}
		for _, reply := range replies {
			if reply.Val() == 0 {
				missing++
			}
		}
	}
	return missing, nil
}
