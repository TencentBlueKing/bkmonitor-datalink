// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

// DefaultTenant is the tenant whose CMDB cache keys carry no tenant segment.
// Every other tenant's are "<tenant>.<platform prefix>.cache.cmdb.*", the rule
// both CMDB cache writers key by (bk-monitor's CMDBCacheManager,
// core/cache/cmdb/base.py:25-44, and the compatible writer's cache_key). A
// record of another tenant's host is not in the default tenant's keys at
// all, so reading it there finds no host: an unknown host, dropped as one.
const DefaultTenant = "system"

// StoreSource is the host index a tenant's records are read against. A
// *Store is one, answering for every tenant: a process or a test that reads
// one cache. TenantStores answers each tenant from its own keys.
type StoreSource interface {
	For(tenant string) *Store
}

// For is the store itself, whatever the tenant.
func (store *Store) For(string) *Store { return store }

// TenantStores holds one host index per tenant: the default tenant's under
// the platform prefix, read before the replica is ready, and another
// tenant's under "<tenant>.<prefix>", read the first time a record or a Plan
// of that tenant asks for it. The read runs on its own, bounded by
// firstLoadBound; a caller on a Slot's path waits for it within its own
// deadline (ForWithin, PrimeTenant), so the first Slot after a start decides on
// it, and a caller without one is answered by the store as it stands,
// never_loaded until the read lands. A load that fails
// leaves the store never_loaded, its records admitted under
// host_facts_unavailable by name, until a refresh reads it. A tenant's index
// is refreshed with the default's from then on and kept for the process's
// life: tenants are few, and forgetting one a Plan of a long period still
// reads would make its next Slot read it again.
type TenantStores struct {
	client  redis.Cmdable
	prefix  string
	options StoreOptions
	base    *Store

	mu      sync.Mutex
	tenants map[string]*Store
	// loaded is closed when a tenant's first read has returned.
	loaded map[string]chan struct{}
}

// firstLoadBound bounds a tenant's first read. A caller waits for it only
// within its own deadline; the read itself is not cut by that caller's
// leaving, so the next Slot finds it done.
const firstLoadBound = 30 * time.Second

func NewTenantStores(client redis.Cmdable, prefix string, options StoreOptions) (*TenantStores, error) {
	reader, err := NewReader(client, prefix)
	if err != nil {
		return nil, err
	}
	base, err := NewStore(reader, options)
	if err != nil {
		return nil, err
	}
	return &TenantStores{client: client, prefix: strings.TrimSuffix(strings.TrimSpace(prefix), "."), options: options,
		base: base, tenants: make(map[string]*Store), loaded: make(map[string]chan struct{})}, nil
}

// Default is the default tenant's store, the one the replica's readiness
// waits on and its health reports.
func (stores *TenantStores) Default() *Store {
	if stores == nil {
		return nil
	}
	return stores.base
}

// For is the tenant's store: the default tenant's for DefaultTenant or no
// tenant; another tenant's, whose first read it starts on its first
// reference and does not wait for: until the read lands the store answers
// as it stands, never_loaded. A caller with a deadline waits through
// ForWithin.
func (stores *TenantStores) For(tenant string) *Store {
	store, _ := stores.lookup(tenant)
	return store
}

// ForWithin is For, waiting for the tenant's first read until ctx ends: the
// caller's deadline, not one of the store's own, bounds how long a Slot
// waits for a tenant it is the first to read. A ctx with no deadline waits
// at most the read's own firstLoadBound.
func (stores *TenantStores) ForWithin(ctx context.Context, tenant string) *Store {
	store, loaded := stores.lookup(tenant)
	if loaded != nil {
		select {
		case <-loaded:
		case <-ctx.Done():
		}
	}
	return store
}

// lookup is the tenant's store and the channel its first read closes, nil for
// the default tenant's, which is read before the replica is ready.
func (stores *TenantStores) lookup(tenant string) (*Store, <-chan struct{}) {
	if stores == nil {
		return nil, nil
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" || tenant == DefaultTenant {
		return stores.base, nil
	}
	stores.mu.Lock()
	if store, held := stores.tenants[tenant]; held {
		loaded := stores.loaded[tenant]
		stores.mu.Unlock()
		return store, loaded
	}
	reader, err := NewReader(stores.client, tenant+"."+stores.prefix)
	if err != nil {
		stores.mu.Unlock()
		return nil, nil
	}
	store, err := NewStore(reader, stores.options)
	if err != nil {
		stores.mu.Unlock()
		return nil, nil
	}
	store.tenant = tenant
	loaded := make(chan struct{})
	stores.tenants[tenant] = store
	stores.loaded[tenant] = loaded
	stores.mu.Unlock()
	go func() {
		defer close(loaded)
		ctx, cancel := context.WithTimeout(context.Background(), firstLoadBound)
		defer cancel()
		_ = store.Refresh(ctx)
	}()
	return store, loaded
}

// PrimeTenant waits, within ctx, for the tenant's first read in whatever
// source a fuller or resolver reads: a TenantStores waits (ForWithin); a
// single Store has nothing to read first.
func PrimeTenant(ctx context.Context, source StoreSource, tenant string) {
	if stores, ok := source.(*TenantStores); ok {
		stores.ForWithin(ctx, tenant)
	}
}

// Refresh refreshes every tenant's store. A store that fails keeps its last
// index and its error, as one store does alone; the default tenant's error
// is returned, joined with the others'.
func (stores *TenantStores) Refresh(ctx context.Context) error {
	if stores == nil {
		return errors.New("alarmd cmdbcache: no tenant stores")
	}
	err := stores.base.Refresh(ctx)
	stores.mu.Lock()
	held := make([]*Store, 0, len(stores.tenants))
	for _, store := range stores.tenants {
		held = append(held, store)
	}
	stores.mu.Unlock()
	for _, store := range held {
		if tenantErr := store.Refresh(ctx); tenantErr != nil {
			err = errors.Join(err, tenantErr)
		}
	}
	return err
}

// Tenants is how many tenants other than the default have a store.
func (stores *TenantStores) Tenants() int {
	if stores == nil {
		return 0
	}
	stores.mu.Lock()
	defer stores.mu.Unlock()
	return len(stores.tenants)
}
