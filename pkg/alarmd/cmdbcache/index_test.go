// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
)

const multiModuleHost = `{"bk_host_id":183016,"bk_host_innerip":"10.0.0.1","bk_cloud_id":0,"bk_biz_id":999,
"bk_state":"运营中[需告警]","display_name":"web-1","topo_link":{
 "module|85":[{"bk_obj_id":"module","bk_inst_id":85},{"bk_obj_id":"set","bk_inst_id":12},{"bk_obj_id":"biz","bk_inst_id":999}],
 "module|91":[{"bk_obj_id":"module","bk_inst_id":91},{"bk_obj_id":"set","bk_inst_id":13},{"bk_obj_id":"biz","bk_inst_id":999}]}}`

// A host in several modules belongs to every node on every one of its links.
// Keeping only one link would put the host outside targets that legitimately
// include it.
func TestAHostCarriesEveryNodeOfEveryTopologyLink(t *testing.T) {
	facts, err := decodeHost(multiModuleHost)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	nodes := map[string]bool{}
	for _, node := range facts.TopoNodes {
		nodes[node] = true
	}
	for _, expected := range []string{"module|85", "module|91", "set|12", "set|13", "biz|999"} {
		if !nodes[expected] {
			t.Errorf("host is missing node %s: %v", expected, facts.TopoNodes)
		}
	}
	if len(facts.TopoNodes) != 5 {
		t.Errorf("node set = %v, want the union of both links with no duplicates", facts.TopoNodes)
	}
	if facts.HostID != "183016" || facts.IP != "10.0.0.1" || facts.CloudID != "0" || facts.State == "" {
		t.Errorf("facts = %+v", facts)
	}
}

// bmw writes each host under two fields. Both must resolve to the same record,
// and the fleet must count once - counting fields reports twice the hosts,
// which is exactly the mistake the sizing measurement made.
func TestBothIdentityShapesResolveToOneHostCountedOnce(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1700000000, 0).UTC())
	builder.addFields([]string{"10.0.0.1|0", multiModuleHost, "183016", multiModuleHost})
	index := builder.index

	if index.Hosts() != 1 {
		t.Fatalf("hosts = %d, want 1", index.Hosts())
	}
	byAddress, foundAddress := index.Lookup("10.0.0.1|0")
	byIdentifier, foundIdentifier := index.Lookup("183016")
	if !foundAddress || !foundIdentifier || byAddress != byIdentifier {
		t.Fatalf("the two identities did not resolve to one record: %v %v", foundAddress, foundIdentifier)
	}
	if _, found := index.Lookup("10.9.9.9|0"); found {
		t.Error("an unknown identity resolved")
	}
}

func TestAMalformedRecordDoesNotBlindTheIndex(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1700000000, 0).UTC())
	builder.addFields([]string{"broken", "{not json", "10.0.0.1|0", multiModuleHost})
	if builder.index.Hosts() != 1 {
		t.Fatalf("hosts = %d, want the readable one", builder.index.Hosts())
	}
}

// Enrichment turns the identity a series arrived with into the topology its
// strategy filters on, and teaches the record the identity it did not carry.
func TestEnrichmentResolvesTopologyAndTheOtherIdentity(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1700000000, 0).UTC())
	builder.addFields([]string{"10.0.0.1|0", multiModuleHost, "183016", multiModuleHost})
	store := &Store{index: builder.index, now: time.Now, maxAge: time.Hour, interval: time.Minute}

	chain := admission.NewChain(
		[]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(store)},
		[]admission.Filter{admission.TargetScopeFilter{}},
	)
	facts := chain.Enrich(map[string]json.RawMessage{
		"bk_target_ip":       json.RawMessage(`"10.0.0.1"`),
		"bk_target_cloud_id": json.RawMessage(`0`),
	})
	if !facts.HostResolved || len(facts.TopoNodes) != 5 {
		t.Fatalf("facts = %+v", facts)
	}
	found := false
	for _, key := range facts.HostKeys {
		if key == "183016" {
			found = true
		}
	}
	if !found {
		t.Errorf("resolving by address did not teach the record its host id: %v", facts.HostKeys)
	}
	if facts.HostState == "" || facts.HostBusinessID != "999" {
		t.Errorf("host attributes were not carried: %+v", facts)
	}
}

// A series whose host is not in the cache must stay unresolved, so a topology
// target rejects it the way Python does.
func TestAnUnknownHostStaysUnresolved(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1700000000, 0).UTC())
	builder.addFields([]string{"10.0.0.1|0", multiModuleHost})
	store := &Store{index: builder.index, now: time.Now, maxAge: time.Hour, interval: time.Minute}
	chain := admission.NewChain([]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(store)}, nil)
	facts := chain.Enrich(map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"10.9.9.9"`)})
	if facts.HostResolved || len(facts.TopoNodes) != 0 {
		t.Fatalf("facts = %+v", facts)
	}
}

type stubLoader struct {
	index *Index
	err   error
}

func (loader stubLoader) Load(context.Context, time.Time) (*Index, error) {
	return loader.index, loader.err
}

// A failed refresh must not drop the filter: the previous index keeps
// answering, and the failure is visible rather than silent.
func TestAFailedRefreshKeepsTheLastGoodIndex(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1700000000, 0).UTC())
	builder.addFields([]string{"10.0.0.1|0", multiModuleHost})
	clock := time.Unix(1700000000, 0).UTC()
	store, err := NewStore(stubLoader{index: builder.index}, StoreOptions{
		RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	store.reader = stubLoader{err: errors.New("redis down")}
	if err := store.Refresh(context.Background()); err == nil {
		t.Fatal("a failing refresh reported success")
	}
	if store.Current() == nil || store.Current().Hosts() != 1 {
		t.Fatal("the last good index was discarded on failure")
	}
	if health := store.Health(); health.Degraded || health.ConsecutiveErrors != 1 {
		t.Fatalf("health = %+v", health)
	}

	clock = clock.Add(11 * time.Minute)
	health := store.Health()
	if !health.Degraded || health.DegradedReason != "index_stale" {
		t.Fatalf("an index past its staleness bound reported %+v", health)
	}
}

// An empty host cache would put every host-scoped strategy out of scope at
// once. That is a degraded read, not a fact about the fleet.
func TestAnEmptyIndexIsReportedAsDegraded(t *testing.T) {
	clock := time.Unix(1700000000, 0).UTC()
	store, err := NewStore(stubLoader{index: newIndexBuilder(clock).index}, StoreOptions{
		RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if health := store.Health(); !health.Degraded || health.DegradedReason != "index_empty" {
		t.Fatalf("health = %+v", health)
	}
}

func TestABeforeFirstLoadStoreIsDegraded(t *testing.T) {
	store, err := NewStore(stubLoader{}, StoreOptions{RefreshInterval: time.Minute, MaxAge: time.Hour})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if health := store.Health(); !health.Degraded || health.DegradedReason != "never_loaded" {
		t.Fatalf("health = %+v", health)
	}
}

// The staleness bound has to leave room for a refresh to happen, otherwise the
// store reports degraded between two healthy reads.
func TestTheStalenessBoundMustExceedTheRefreshInterval(t *testing.T) {
	if _, err := NewStore(stubLoader{}, StoreOptions{RefreshInterval: time.Minute, MaxAge: time.Minute}); err == nil {
		t.Fatal("a bound equal to the interval was accepted")
	}
}

func TestRefreshedAtAcceptsTheShapesTheMarkerHasUsed(t *testing.T) {
	seconds := parseRefreshedAt("1788958800")
	millis := parseRefreshedAt("1788958800000")
	stamp := parseRefreshedAt("2026-09-09T13:00:00Z")
	if seconds.IsZero() || millis.IsZero() || stamp.IsZero() {
		t.Fatalf("parsed %v %v %v", seconds, millis, stamp)
	}
	if !seconds.Equal(millis) || !seconds.Equal(stamp) {
		t.Fatalf("the three encodings disagree: %v %v %v", seconds, millis, stamp)
	}
	if !parseRefreshedAt("").IsZero() || !parseRefreshedAt("not a time").IsZero() {
		t.Error("an unreadable marker was treated as a time")
	}
}

func TestReaderRequiresAPlatformPrefix(t *testing.T) {
	if _, err := NewReader(nil, "platform.prefix"); err == nil {
		t.Error("a reader without a client was accepted")
	}
	reader, err := NewReader(stubCmdable{}, " platform.prefix. ")
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	if key := reader.hostKey(); key != "platform.prefix.cache.cmdb.host" {
		t.Errorf("host key = %s", key)
	}
	if key := reader.refreshedKey(); !strings.HasSuffix(key, "cache.cmdb_last_refresh_all_time.host_topo") {
		t.Errorf("refreshed key = %s", key)
	}
}

// Two hosts that a single record names at once: its address belongs to a host
// the platform marks as not monitored, its host id to one that is monitored.
// Python looks a host up by its id whenever the record carries one and never
// falls back to the address, so the state a filter acts on is the id's.
const disabledByAddressHost = `{"bk_host_id":700001,"bk_host_innerip":"10.0.0.7","bk_cloud_id":0,"bk_biz_id":999,
"bk_state":"备用机","display_name":"spare","topo_link":{
 "module|85":[{"bk_obj_id":"module","bk_inst_id":85},{"bk_obj_id":"biz","bk_inst_id":999}]}}`

const monitoredByIDHost = `{"bk_host_id":700002,"bk_host_innerip":"10.0.0.8","bk_cloud_id":0,"bk_biz_id":999,
"bk_state":"运营中[需告警]","display_name":"live","topo_link":{
 "module|91":[{"bk_obj_id":"module","bk_inst_id":91},{"bk_obj_id":"biz","bk_inst_id":999}]}}`

// Taking whichever identity resolved first would read the spare host's state
// and drop a series Python keeps. The address is still resolved - the target
// scope matches on either identity - only the attributes follow Python.
func TestHostAttributesFollowTheIdentityPythonWouldLookUp(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1700000000, 0).UTC())
	builder.addFields([]string{"10.0.0.7|0", disabledByAddressHost, "700001", disabledByAddressHost,
		"10.0.0.8|0", monitoredByIDHost, "700002", monitoredByIDHost})
	store := &Store{index: builder.index, now: time.Now, maxAge: time.Hour, interval: time.Minute}

	chain := admission.NewChain(
		[]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(store)},
		[]admission.Filter{admission.TargetScopeFilter{}},
	)
	facts := chain.Enrich(map[string]json.RawMessage{
		"bk_target_ip":       json.RawMessage(`"10.0.0.7"`),
		"bk_target_cloud_id": json.RawMessage(`0`),
		"bk_host_id":         json.RawMessage(`700002`),
	})
	if !facts.HostResolved {
		t.Fatalf("facts = %+v", facts)
	}
	if facts.HostState != "运营中[需告警]" {
		t.Fatalf("host state = %q, want the state of the host the id names", facts.HostState)
	}
	// Both identities still resolve, because a monitoring target may name
	// either one.
	address, id := false, false
	for _, key := range facts.HostKeys {
		if key == "10.0.0.7|0" {
			address = true
		}
		if key == "700002" {
			id = true
		}
	}
	if !address || !id {
		t.Fatalf("host keys = %v, want both identities kept for target matching", facts.HostKeys)
	}
}

// Without a host id the address is what Python looks up, so its state is the
// one that counts.
func TestHostAttributesComeFromTheAddressWhenNoIDIsNamed(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1700000000, 0).UTC())
	builder.addFields([]string{"10.0.0.7|0", disabledByAddressHost, "700001", disabledByAddressHost})
	store := &Store{index: builder.index, now: time.Now, maxAge: time.Hour, interval: time.Minute}

	chain := admission.NewChain(
		[]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(store)},
		[]admission.Filter{admission.TargetScopeFilter{}},
	)
	facts := chain.Enrich(map[string]json.RawMessage{
		"bk_target_ip":       json.RawMessage(`"10.0.0.7"`),
		"bk_target_cloud_id": json.RawMessage(`0`),
	})
	if facts.HostState != "备用机" {
		t.Fatalf("host state = %q, want the address's host", facts.HostState)
	}
}
