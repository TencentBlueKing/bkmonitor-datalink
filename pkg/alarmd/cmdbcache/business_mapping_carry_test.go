// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

const clusterMappingKey = "bk_monitorv3.ce.cache.cmdb.bcs_cluster_business"

// The writer's contract, from the CMDB cache publisher (publish_hashes and
// its sources): every round renames a staging hash over the key and deletes
// its placeholder, and leaves a mapping whose query failed out of the round,
// so the one before stands until its TTL. It cannot state an empty mapping -
// a hash with no fields does not exist - so a missing key is no clusters, a
// mapping left out of its rounds past its TTL, or no publisher at all. The
// reader rule: a hash that is there is read as written; a missing key maps
// nothing and is named missing, the writer's TTL having already carried its
// last good mapping; a key that cannot be read is the reader's own error, so
// the last mapping read stands, named read_failed, until a read succeeds.

// mappingClient is a hash client whose scans of one key can be made to fail.
type mappingClient struct {
	*hashClient
	failing map[string]bool
}

func (client *mappingClient) HScan(ctx context.Context, key string, cursor uint64, match string, count int64) *redis.ScanCmd {
	if client.failing[key] {
		return redis.NewScanCmdResult(nil, 0, errors.New("scan failed"))
	}
	return client.hashClient.HScan(ctx, key, cursor, match, count)
}

// refreshingMappingStore is a store over a hash client the test rewrites
// between refreshes, with a staleness bound of an hour and a clock the test
// moves past it.
func refreshingMappingStore(t *testing.T, client redis.Cmdable) (*Store, *time.Time) {
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
	return store, &at
}

func mustRefresh(t *testing.T, store *Store) {
	t.Helper()
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
}

// A missing key after a hash with entries maps nothing at the next load,
// named missing, and the mapping is back as soon as the hash is.
func TestAMissingMappingMapsNothingNamedMissing(t *testing.T) {
	client := &hashClient{hashes: map[string][]string{clusterMappingKey: {"BCS-K8S-00001", "11"}}}
	store, _ := refreshingMappingStore(t, client)
	mustRefresh(t, store)
	lookup := NewHostBusinessLookup(store)

	delete(client.hashes, clusterMappingKey)
	mustRefresh(t, store)
	if stats := store.Health().ClusterBusinessMapping; stats != (MappingStats{Missing: true}) {
		t.Fatalf("missing: %+v, want nothing held and missing named", stats)
	}
	if _, found := lookup.LookupClusterBusiness("BCS-K8S-00001"); found {
		t.Fatal("an entry of a missing mapping was carried")
	}

	client.hashes[clusterMappingKey] = []string{"BCS-K8S-00002", "12"}
	mustRefresh(t, store)
	if stats := store.Health().ClusterBusinessMapping; stats != (MappingStats{Held: 1}) {
		t.Fatalf("mapping back: %+v, want one entry and no flag", stats)
	}
}

// The namespace mapping follows the same rule: its key gone after a hash with
// entries maps nothing at the next load, named missing, and the cluster
// mapping beside it is read as it is.
func TestAMissingNamespaceMappingMapsNothingNamedMissing(t *testing.T) {
	const namespaceKey = "bk_monitorv3.ce.cache.cmdb.bcs_namespace_business"
	client := &hashClient{hashes: map[string][]string{
		clusterMappingKey: {"BCS-K8S-00001", "11"},
		namespaceKey:      {"BCS-K8S-00001|prod", "21"},
	}}
	store, _ := refreshingMappingStore(t, client)
	mustRefresh(t, store)
	lookup := NewHostBusinessLookup(store)
	if business, found := lookup.LookupNamespaceBusiness("BCS-K8S-00001", "prod"); !found || business != "21" {
		t.Fatalf("namespace prod before = %q, %v; want 21", business, found)
	}

	delete(client.hashes, namespaceKey)
	mustRefresh(t, store)
	health := store.Health()
	if health.NamespaceBusinessMapping != (MappingStats{Missing: true}) || health.ClusterBusinessMapping != (MappingStats{Held: 1}) {
		t.Fatalf("namespace %+v, cluster %+v; want the namespace mapping missing and the cluster mapping read",
			health.NamespaceBusinessMapping, health.ClusterBusinessMapping)
	}
	if _, found := lookup.LookupNamespaceBusiness("BCS-K8S-00001", "prod"); found {
		t.Fatal("an entry of a missing namespace mapping was carried")
	}
	if business, found := lookup.LookupClusterBusiness("BCS-K8S-00001"); !found || business != "11" {
		t.Fatalf("cluster beside a missing namespace mapping = %q, %v; want 11", business, found)
	}
}

// A key that cannot be read keeps the last mapping read, its counts included,
// named read_failed, for as long as reads fail - past the index's staleness
// bound too - and the next read that succeeds replaces it and clears the flag.
func TestAMappingThatCannotBeReadKeepsTheLastReadUntilAReadSucceeds(t *testing.T) {
	client := &mappingClient{hashClient: &hashClient{hashes: map[string][]string{
		clusterMappingKey: {"BCS-K8S-00001", "11", "BCS-K8S-00009", "0"}}}, failing: map[string]bool{}}
	store, now := refreshingMappingStore(t, client)
	mustRefresh(t, store)
	lookup := NewHostBusinessLookup(store)

	client.failing[clusterMappingKey] = true
	start := *now
	for _, after := range []time.Duration{time.Minute, 59 * time.Minute, 2 * time.Hour, 24 * time.Hour} {
		*now = start.Add(after)
		mustRefresh(t, store)
		if stats := store.Health().ClusterBusinessMapping; stats != (MappingStats{Held: 1, Refused: 1, ReadFailed: true}) {
			t.Fatalf("unreadable at +%v: %+v, want the last read kept and read_failed named", after, stats)
		}
		if business, found := lookup.LookupClusterBusiness("BCS-K8S-00001"); !found || business != "11" {
			t.Fatalf("kept cluster at +%v = %q, %v; want 11", after, business, found)
		}
	}

	client.failing[clusterMappingKey] = false
	client.hashes[clusterMappingKey] = []string{"BCS-K8S-00002", "12"}
	mustRefresh(t, store)
	if stats := store.Health().ClusterBusinessMapping; stats != (MappingStats{Held: 1}) {
		t.Fatalf("readable again: %+v, want the new read and no flag", stats)
	}
	if _, found := lookup.LookupClusterBusiness("BCS-K8S-00001"); found {
		t.Fatal("an entry of the kept mapping survived a read that succeeded")
	}
}

// A key that cannot be read after a missing one keeps nothing: the last read
// found no hash. It is named read_failed, not missing - this load did not
// find out.
func TestAnUnreadableMappingAfterAMissingOneHoldsNothing(t *testing.T) {
	client := &mappingClient{hashClient: &hashClient{hashes: map[string][]string{}}, failing: map[string]bool{}}
	store, _ := refreshingMappingStore(t, client)
	mustRefresh(t, store)
	client.failing[clusterMappingKey] = true
	mustRefresh(t, store)
	if stats := store.Health().ClusterBusinessMapping; stats != (MappingStats{ReadFailed: true}) {
		t.Fatalf("unreadable after missing: %+v, want nothing held and read_failed only", stats)
	}
}

// A hash whose every field is refused is there: it is read as written, with
// nothing usable, and its refusals counted. It is not missing, and nothing
// before it is carried.
func TestAMappingWhoseEveryFieldIsRefusedIsReadAsWritten(t *testing.T) {
	client := &hashClient{hashes: map[string][]string{clusterMappingKey: {"BCS-K8S-00001", "11"}}}
	store, _ := refreshingMappingStore(t, client)
	mustRefresh(t, store)

	client.hashes[clusterMappingKey] = []string{"BCS-K8S-00002", "0", "BCS-K8S-00003", "not-a-business"}
	mustRefresh(t, store)
	if stats := store.Health().ClusterBusinessMapping; stats != (MappingStats{Refused: 2}) {
		t.Fatalf("mapping with every field refused: %+v, want nothing held and this load's two refusals", stats)
	}
}

// A mapping never published is missing from the first load, named.
func TestAMappingNeverPublishedIsMissing(t *testing.T) {
	store, _ := refreshingMappingStore(t, &hashClient{hashes: map[string][]string{}})
	mustRefresh(t, store)
	mustRefresh(t, store)
	if stats := store.Health().ClusterBusinessMapping; stats != (MappingStats{Missing: true}) {
		t.Fatalf("mapping never published: %+v, want nothing held and missing named", stats)
	}
}

// Between two loads that both hold entries the later one replaces the
// earlier, entries dropped included.
func TestAMappingThatChangesBetweenLoadsIsReplaced(t *testing.T) {
	client := &hashClient{hashes: map[string][]string{clusterMappingKey: {"BCS-K8S-00001", "11", "BCS-K8S-00002", "12"}}}
	store, _ := refreshingMappingStore(t, client)
	mustRefresh(t, store)
	client.hashes[clusterMappingKey] = []string{"BCS-K8S-00002", "13"}
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
