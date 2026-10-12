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
// of that tenant asks for it - on that Slot's path, once, as a dynamic group
// is - so the first Slot after a start decides on it. A load that fails
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
}

// firstLoadBound bounds the load a tenant's first reference makes on its
// Slot's path.
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
		base: base, tenants: make(map[string]*Store)}, nil
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
// tenant; another tenant's, read on its first reference. The first
// reference's caller waits for that read; a caller that asks while it runs
// is answered by the store as it stands, never_loaded.
func (stores *TenantStores) For(tenant string) *Store {
	if stores == nil {
		return nil
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" || tenant == DefaultTenant {
		return stores.base
	}
	stores.mu.Lock()
	if store, held := stores.tenants[tenant]; held {
		stores.mu.Unlock()
		return store
	}
	reader, err := NewReader(stores.client, tenant+"."+stores.prefix)
	if err != nil {
		stores.mu.Unlock()
		return nil
	}
	store, err := NewStore(reader, stores.options)
	if err != nil {
		stores.mu.Unlock()
		return nil
	}
	stores.tenants[tenant] = store
	stores.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), firstLoadBound)
	defer cancel()
	_ = store.Refresh(ctx)
	return store
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
