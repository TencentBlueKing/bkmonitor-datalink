// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"testing"
	"time"
)

const emptiedClusterKey = "bk_monitorv3.ce.cache.cmdb.bcs_cluster_business"

// refreshingMappingStore is a store over a hash client the test rewrites
// between refreshes.
func refreshingMappingStore(t *testing.T, client *hashClient) *Store {
	t.Helper()
	reader, err := NewReader(client, "bk_monitorv3.ce")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1700000000, 0).UTC()
	store, err := NewStore(reader, StoreOptions{RefreshInterval: time.Minute, MaxAge: time.Hour, Now: func() time.Time { return at }})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func mustRefresh(t *testing.T, store *Store) {
	t.Helper()
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
}

// The writer publishes an empty mapping by deleting the hash, which is also
// what a source that answered nothing looks like. Read empty after a load
// that held entries, the mapping keeps those entries, flagged emptied; a
// load that holds entries again replaces them and lowers the flag.
func TestAMappingReadEmptyAfterOneThatHeldEntriesKeepsThem(t *testing.T) {
	client := &hashClient{hashes: map[string][]string{emptiedClusterKey: {"BCS-K8S-00001", "11"}}}
	store := refreshingMappingStore(t, client)
	mustRefresh(t, store)

	delete(client.hashes, emptiedClusterKey)
	mustRefresh(t, store)
	if stats := store.Health().ClusterBusinessMapping; stats != (MappingStats{Held: 1, Emptied: true}) {
		t.Fatalf("mapping read empty after one held: %+v, want the one entry carried and emptied up", stats)
	}
	lookup := NewHostBusinessLookup(store)
	if business, found := lookup.LookupClusterBusiness("BCS-K8S-00001"); !found || business != "11" {
		t.Fatalf("carried cluster = %q, %v; want 11", business, found)
	}
	mustRefresh(t, store)
	if stats := store.Health().ClusterBusinessMapping; !stats.Emptied || stats.Held != 1 {
		t.Fatalf("a second empty read: %+v, want the entry still carried", stats)
	}

	client.hashes[emptiedClusterKey] = []string{"BCS-K8S-00002", "12"}
	mustRefresh(t, store)
	if stats := store.Health().ClusterBusinessMapping; stats != (MappingStats{Held: 1}) {
		t.Fatalf("mapping held again: %+v, want one entry and no flag", stats)
	}
	if _, found := lookup.LookupClusterBusiness("BCS-K8S-00001"); found {
		t.Fatal("the carried entry outlived a load that held entries of its own")
	}
	if business, found := lookup.LookupClusterBusiness("BCS-K8S-00002"); !found || business != "12" {
		t.Fatalf("new cluster = %q, %v; want 12", business, found)
	}
}

// A hash whose every field is refused holds no entry either, and the
// entries before are carried the same way; what this load refused is its
// own count, so a writer publishing only values that are not a business
// reads apart from one that published nothing.
func TestAMappingWhoseEveryFieldIsRefusedKeepsTheEntriesAndCountsTheRefusal(t *testing.T) {
	client := &hashClient{hashes: map[string][]string{emptiedClusterKey: {"BCS-K8S-00001", "11"}}}
	store := refreshingMappingStore(t, client)
	mustRefresh(t, store)

	client.hashes[emptiedClusterKey] = []string{"BCS-K8S-00002", "0", "BCS-K8S-00003", "not-a-business"}
	mustRefresh(t, store)
	if stats := store.Health().ClusterBusinessMapping; stats != (MappingStats{Held: 1, Refused: 2, Emptied: true}) {
		t.Fatalf("mapping with every field refused: %+v, want the one entry carried and this load's two refusals", stats)
	}
}

// A mapping never held stays empty: that is a writer not publishing it yet,
// not one that lost it.
func TestAMappingEmptyOnEveryLoadStaysEmpty(t *testing.T) {
	store := refreshingMappingStore(t, &hashClient{hashes: map[string][]string{}})
	mustRefresh(t, store)
	mustRefresh(t, store)
	if stats := store.Health().ClusterBusinessMapping; stats != (MappingStats{}) {
		t.Fatalf("mapping never published: %+v, want nothing held and no flag", stats)
	}
}

// Between two loads that both hold entries the later one replaces the
// earlier, entries dropped included.
func TestAMappingThatChangesBetweenLoadsIsReplaced(t *testing.T) {
	client := &hashClient{hashes: map[string][]string{emptiedClusterKey: {"BCS-K8S-00001", "11", "BCS-K8S-00002", "12"}}}
	store := refreshingMappingStore(t, client)
	mustRefresh(t, store)
	client.hashes[emptiedClusterKey] = []string{"BCS-K8S-00002", "13"}
	mustRefresh(t, store)
	if stats := store.Health().ClusterBusinessMapping; stats != (MappingStats{Held: 1}) {
		t.Fatalf("mapping after the change: %+v, want the one entry of the later load", stats)
	}
	lookup := NewHostBusinessLookup(store)
	if _, found := lookup.LookupClusterBusiness("BCS-K8S-00001"); found {
		t.Fatal("an entry the later load dropped was kept")
	}
	if business, found := lookup.LookupClusterBusiness("BCS-K8S-00002"); !found || business != "13" {
		t.Fatalf("changed cluster = %q, %v; want 13", business, found)
	}
}
