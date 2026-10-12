// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// The platform's own dynamic group cache is one hash per tenant beside the
// host hash, "<platform prefix>.cache.cmdb.dynamic_group" (another tenant's
// with the tenant in front, the rule of the host hash): the field is the
// group id, the value {"bk_obj_id", "bk_inst_ids", "bk_biz_id", "name", "id"}.
// bk-monitor-worker's CMDB cache writes it on its full refresh and removes the
// groups CMDB no longer lists; Python reads it when it matches a dynamic group
// target (DynamicGroupManager.mget in bkmonitor/utils/range/target.py), and
// only a host group has members it matches, its bk_inst_ids being host ids.
const dynamicGroupCacheSuffix = "cache.cmdb.dynamic_group"

// platformGroupModel is the one object a platform group's members are
// matched as.
const platformGroupModel = "host"

// platformGroupBatch is the most group ids one HMGET names, and
// platformGroupLengthBatch the most one pipeline of HSTRLEN does.
const (
	platformGroupBatch       = 64
	platformGroupLengthBatch = 512
)

// PlatformGroupReader reads the platform's dynamic group hash of one tenant.
type PlatformGroupReader struct {
	client redis.Cmdable
	key    string
}

// NewPlatformGroupReader reads the tenant's hash under the platform prefix:
// the default tenant's carries no tenant segment.
func NewPlatformGroupReader(client redis.Cmdable, prefix, tenant string) (*PlatformGroupReader, error) {
	if client == nil {
		return nil, errors.New("alarmd cmdbcache: a redis client is required")
	}
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), ".")
	if prefix == "" {
		return nil, errors.New("alarmd cmdbcache: a non-empty platform key prefix is required")
	}
	if tenant = strings.TrimSpace(tenant); tenant != "" && tenant != DefaultTenant {
		prefix = tenant + "." + prefix
	}
	return &PlatformGroupReader{client: client, key: prefix + "." + dynamicGroupCacheSuffix}, nil
}

// Read reads the ids a window at a time, handing each id's value to visit
// before the next window is read. A host group's value is its whole host
// list - a few hundred bytes to megabytes - so a window is bounded as a fork
// read's is: the values' lengths are read first (HSTRLEN, pipelined
// platformGroupLengthBatch at a time), and one HMGET names the ids after the
// last whose lengths add up to at most bound bytes, at most
// platformGroupBatch of them, or the one value larger than bound alone. A
// field the hash does not have, or a hash that is not there, is an answer:
// the group is missing. A reply Redis answers with an error - WRONGTYPE,
// LOADING - or that does not complete stops the read with the error, as a
// fork read does.
func (reader *PlatformGroupReader) Read(ctx context.Context, ids []string, bound int, visit func(id string, read GroupRead)) error {
	for start := 0; start < len(ids); {
		lengths, err := reader.lengths(ctx, ids[start:min(start+platformGroupLengthBatch, len(ids))])
		if err != nil {
			return err
		}
		for offset := 0; offset < len(lengths); {
			end, held := offset, int64(0)
			for end < len(lengths) && end-offset < platformGroupBatch && (end == offset || held+lengths[end] <= int64(bound)) {
				held += lengths[end]
				end++
			}
			if err := reader.window(ctx, ids[start+offset:start+end], visit); err != nil {
				return err
			}
			offset = end
		}
		start += len(lengths)
	}
	return nil
}

// lengths reads the values' lengths of ids in one pipeline; a field the hash
// does not have is 0.
func (reader *PlatformGroupReader) lengths(ctx context.Context, ids []string) ([]int64, error) {
	commands := make([]*redis.Cmd, len(ids))
	if _, err := reader.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for index, id := range ids {
			// go-redis v8 has no HSTRLEN helper.
			commands[index] = pipe.Do(ctx, "hstrlen", reader.key, id)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("alarmd cmdbcache: read platform dynamic group lengths: %w", err)
	}
	lengths := make([]int64, len(ids))
	for index, command := range commands {
		length, err := command.Int64()
		if err != nil {
			return nil, fmt.Errorf("alarmd cmdbcache: read platform dynamic group lengths: %w", err)
		}
		lengths[index] = length
	}
	return lengths, nil
}

// window reads one window's values with one HMGET.
func (reader *PlatformGroupReader) window(ctx context.Context, ids []string, visit func(id string, read GroupRead)) error {
	values, err := reader.client.HMGet(ctx, reader.key, ids...).Result()
	if err != nil {
		return fmt.Errorf("alarmd cmdbcache: read platform dynamic groups: %w", err)
	}
	if len(values) != len(ids) {
		return fmt.Errorf("alarmd cmdbcache: read platform dynamic groups: %d values for %d ids", len(values), len(ids))
	}
	for index, value := range values {
		text, held := value.(string)
		if !held {
			visit(ids[index], GroupRead{Missing: true})
			continue
		}
		visit(ids[index], GroupRead{Payload: []byte(text)})
	}
	return nil
}

// decode reads one platform group (decodePlatformGroup).
func (reader *PlatformGroupReader) decode(id string, payload []byte, readAt time.Time) *GroupSnapshot {
	return decodePlatformGroup(id, payload, readAt)
}

// decodePlatformGroup reads one value of the platform's hash: the group's
// object, and its instance ids as members. An absent bk_inst_ids is a
// structure the reader does not accept - it cannot tell "empty" from "not
// written" - and a present, empty one is the writer saying the group holds
// nothing, as CMDB answered it. A host group's members carry their host id;
// another object's do not, and such a group matches no host, as in Python.
func decodePlatformGroup(id string, payload []byte, readAt time.Time) *GroupSnapshot {
	snapshot := &GroupSnapshot{ID: id, ReadAt: readAt}
	var document struct {
		ObjectID  string             `json:"bk_obj_id"`
		Instances *[]json.RawMessage `json:"bk_inst_ids"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		snapshot.Unavailable = targetplan.ReasonJSONInvalid
		return snapshot
	}
	if strings.TrimSpace(document.ObjectID) == "" || document.Instances == nil {
		snapshot.Unavailable = targetplan.ReasonStructureInvalid
		return snapshot
	}
	snapshot.ModelID = strings.TrimSpace(document.ObjectID)
	for _, raw := range *document.Instances {
		instance := rawScalarText(raw)
		if instance == "" {
			snapshot.Dropped++
			continue
		}
		member := GroupMember{ModelID: snapshot.ModelID, ModelInstID: instance}
		if snapshot.ModelID == platformGroupModel {
			member.HostID = instance
		}
		snapshot.Members = append(snapshot.Members, member)
	}
	return snapshot
}

// PlatformGroupStores holds the platform's dynamic groups a Slot has asked
// for, one store per tenant: a tenant's store, and its first group, are read
// when a Plan of that tenant first names a group, and refreshed on the host
// index's cadence while Plans keep asking. A deployment whose strategies name
// no dynamic group reads nothing.
type PlatformGroupStores struct {
	client  redis.Cmdable
	prefix  string
	options GroupStoreOptions

	mu      sync.Mutex
	tenants map[string]*GroupStore
}

// NewPlatformGroupStores reads the platform's hashes under prefix with the
// host index's cadence and staleness bound, one read holding at most
// options.ReadBound bytes of group values (PlatformGroupReader.Read).
func NewPlatformGroupStores(client redis.Cmdable, prefix string, options GroupStoreOptions) (*PlatformGroupStores, error) {
	stores := &PlatformGroupStores{client: client, prefix: prefix, options: options, tenants: make(map[string]*GroupStore)}
	// The default tenant's store is built here so a coordinate or an option
	// that cannot work is refused at start, not on a Slot.
	if _, err := stores.store(DefaultTenant); err != nil {
		return nil, err
	}
	return stores, nil
}

func (stores *PlatformGroupStores) store(tenant string) (*GroupStore, error) {
	if tenant = strings.TrimSpace(tenant); tenant == "" {
		tenant = DefaultTenant
	}
	stores.mu.Lock()
	defer stores.mu.Unlock()
	if store, held := stores.tenants[tenant]; held {
		return store, nil
	}
	reader, err := NewPlatformGroupReader(stores.client, stores.prefix, tenant)
	if err != nil {
		return nil, err
	}
	store, err := newGroupStore(reader, stores.options)
	if err != nil {
		return nil, err
	}
	stores.tenants[tenant] = store
	return store, nil
}

func (stores *PlatformGroupStores) held() []*GroupStore {
	stores.mu.Lock()
	defer stores.mu.Unlock()
	held := make([]*GroupStore, 0, len(stores.tenants))
	for _, store := range stores.tenants {
		held = append(held, store)
	}
	return held
}

// ResolveScopeGroup answers one group a Plan's target names, for one Slot,
// in the words a target plan's group selector answers in: unavailable by
// name when it was not read, is not in the hash, does not decode, is not a
// group of hosts, or is past the staleness bound; otherwise its host ids,
// with the age of a snapshot served past a failed refresh.
func (stores *PlatformGroupStores) ResolveScopeGroup(ctx context.Context, tenant, id string, interval time.Duration) targetplan.SelectorResult {
	result := targetplan.SelectorResult{Kind: targetplan.SelectorKindGroup, ID: id, Reason: targetplan.ReasonNone}
	if stores == nil {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonSourceUnwired
		return result
	}
	store, err := stores.store(tenant)
	if err != nil {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonSourceUnwired
		return result
	}
	lookup := store.Group(ctx, id, interval)
	if lookup.ReadErr != nil || lookup.Snapshot == nil {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonReadFailed
		return result
	}
	snapshot := lookup.Snapshot
	switch {
	case snapshot.Unavailable != "":
		result.State, result.Reason = targetplan.SelectorUnavailable, snapshot.Unavailable
		return result
	case lookup.Age > store.MaxAge():
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonStale
		return result
	case snapshot.ModelID != platformGroupModel:
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonModelMismatch
		return result
	}
	if lookup.RefreshFailed {
		result.StaleAge = lookup.Age
	}
	result.Members = make(map[string]struct{}, len(snapshot.Members))
	for _, member := range snapshot.Members {
		result.Members[member.HostID] = struct{}{}
	}
	result.Kept, result.Dropped = len(result.Members), snapshot.Dropped
	switch {
	case result.Dropped > 0:
		result.State, result.Reason = targetplan.SelectorIncomplete, targetplan.ReasonMembersDropped
	case len(result.Members) == 0:
		result.State = targetplan.SelectorOKEmpty
	default:
		result.State = targetplan.SelectorOK
	}
	return result
}

// Refresh refreshes every tenant's groups; the errors are joined.
func (stores *PlatformGroupStores) Refresh(ctx context.Context) error {
	if stores == nil {
		return nil
	}
	var err error
	for _, store := range stores.held() {
		if storeErr := store.Refresh(ctx); storeErr != nil {
			err = errors.Join(err, storeErr)
		}
	}
	return err
}

// Run keeps the referenced groups fresh until the context ends.
func (stores *PlatformGroupStores) Run(ctx context.Context) {
	if stores == nil {
		return
	}
	ticker := time.NewTicker(stores.options.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = stores.Refresh(ctx)
		}
	}
}
