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

const multiModuleHost = `{"bk_host_id":183016,"bk_host_innerip":"192.0.2.141","bk_cloud_id":0,"bk_biz_id":999,
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
	if facts.HostID != "183016" || facts.IP != "192.0.2.141" || facts.CloudID != "0" || facts.State == "" {
		t.Errorf("facts = %+v", facts)
	}
}

// bmw writes each host under two fields. Both must resolve to the same record,
// and the fleet must count once - counting fields reports twice the hosts,
// which is exactly the mistake the sizing measurement made.
func TestBothIdentityShapesResolveToOneHostCountedOnce(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1700000000, 0).UTC())
	builder.addFields([]string{"192.0.2.141|0", multiModuleHost, "183016", multiModuleHost})
	index := builder.index

	if index.Hosts() != 1 {
		t.Fatalf("hosts = %d, want 1", index.Hosts())
	}
	byAddress, foundAddress := index.Lookup("192.0.2.141|0")
	byIdentifier, foundIdentifier := index.Lookup("183016")
	if !foundAddress || !foundIdentifier || byAddress != byIdentifier {
		t.Fatalf("the two identities did not resolve to one record: %v %v", foundAddress, foundIdentifier)
	}
	if _, found := index.Lookup("192.0.2.199|0"); found {
		t.Error("an unknown identity resolved")
	}
}

func TestAMalformedRecordDoesNotBlindTheIndex(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1700000000, 0).UTC())
	builder.addFields([]string{"broken", "{not json", "192.0.2.141|0", multiModuleHost})
	if builder.index.Hosts() != 1 {
		t.Fatalf("hosts = %d, want the readable one", builder.index.Hosts())
	}
}

// Enrichment turns the identity a series arrived with into the topology its
// strategy filters on, and teaches the record the identity it did not carry.
func TestEnrichmentResolvesTopologyAndTheOtherIdentity(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1700000000, 0).UTC())
	builder.addFields([]string{"192.0.2.141|0", multiModuleHost, "183016", multiModuleHost})
	store := &Store{index: builder.index, now: builder.index.BuiltAt, maxAge: time.Hour}

	chain := admission.NewChain(
		[]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(store)},
		[]admission.Filter{admission.TargetScopeFilter{}},
	)
	facts := chain.Enrich(map[string]json.RawMessage{
		"bk_target_ip":       json.RawMessage(`"192.0.2.141"`),
		"bk_target_cloud_id": json.RawMessage(`0`),
	})
	if !facts.HostResolved || len(facts.TopoNodes()) != 5 {
		t.Fatalf("facts = %+v", facts)
	}
	found := false
	for _, key := range facts.HostKeys() {
		if key == "183016" {
			found = true
		}
	}
	if !found {
		t.Errorf("resolving by address did not teach the record its host id: %v", facts.HostKeys())
	}
	if facts.HostState == "" || facts.HostBusinessID != "999" {
		t.Errorf("host attributes were not carried: %+v", facts)
	}
}

// A series whose host is not in the cache must stay unresolved, so a topology
// target rejects it the way Python does.
func TestAnUnknownHostStaysUnresolved(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1700000000, 0).UTC())
	builder.addFields([]string{"192.0.2.141|0", multiModuleHost})
	store := &Store{index: builder.index, now: builder.index.BuiltAt, maxAge: time.Hour}
	chain := admission.NewChain([]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(store)}, nil)
	facts := chain.Enrich(map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"192.0.2.199"`)})
	if facts.HostResolved || len(facts.TopoNodes()) != 0 {
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
	builder.addFields([]string{"192.0.2.141|0", multiModuleHost})
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
	if health := store.Health(); health.Degraded || store.lastError == nil {
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

func TestAWritersTimeMarkerIsEpochSeconds(t *testing.T) {
	if got := parseEpochSeconds("1788958800"); !got.Equal(time.Unix(1788958800, 0)) {
		t.Fatalf("parsed %v", got)
	}
	for _, value := range []string{"", "not a time", "-5", "0", "1788958800000x", "2026-09-09T13:00:00Z"} {
		if !parseEpochSeconds(value).IsZero() {
			t.Errorf("marker %q was treated as a time", value)
		}
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
const disabledByAddressHost = `{"bk_host_id":700001,"bk_host_innerip":"192.0.2.147","bk_cloud_id":0,"bk_biz_id":999,
"bk_state":"备用机","display_name":"spare","topo_link":{
 "module|85":[{"bk_obj_id":"module","bk_inst_id":85},{"bk_obj_id":"biz","bk_inst_id":999}]}}`

const monitoredByIDHost = `{"bk_host_id":700002,"bk_host_innerip":"192.0.2.148","bk_cloud_id":0,"bk_biz_id":999,
"bk_state":"运营中[需告警]","display_name":"live","topo_link":{
 "module|91":[{"bk_obj_id":"module","bk_inst_id":91},{"bk_obj_id":"biz","bk_inst_id":999}]}}`

// Taking whichever identity resolved first would read the spare host's state
// and drop a series Python keeps. Python looks the host up by the id and,
// having found it, writes that host's own address over the record's and
// returns (fullers.py:61-74); its target match then reads the written values
// (target.py:112-120). So the keys are the id and the id's host's address,
// and the record's own address - another host's - is not one of them.
func TestHostFactsFollowTheIdentityPythonWouldLookUp(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1700000000, 0).UTC())
	builder.addFields([]string{"192.0.2.147|0", disabledByAddressHost, "700001", disabledByAddressHost,
		"192.0.2.148|0", monitoredByIDHost, "700002", monitoredByIDHost})
	store := &Store{index: builder.index, now: builder.index.BuiltAt, maxAge: time.Hour}

	chain := admission.NewChain(
		[]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(store)},
		[]admission.Filter{admission.TargetScopeFilter{}},
	)
	facts := chain.Enrich(map[string]json.RawMessage{
		"bk_target_ip":       json.RawMessage(`"192.0.2.147"`),
		"bk_target_cloud_id": json.RawMessage(`0`),
		"bk_host_id":         json.RawMessage(`700002`),
	})
	if !facts.HostResolved {
		t.Fatalf("facts = %+v", facts)
	}
	if facts.HostState != "运营中[需告警]" {
		t.Fatalf("host state = %q, want the state of the host the id names", facts.HostState)
	}
	keys := map[string]bool{}
	for _, key := range facts.HostKeys() {
		keys[key] = true
	}
	if len(keys) != 2 || !keys["700002"] || !keys["192.0.2.148|0"] {
		t.Fatalf("host keys = %v, want exactly the id and its host's own address", facts.HostKeys())
	}
	if nodes := facts.TopoNodes(); len(nodes) == 0 || containsNode(nodes, "module|85") {
		t.Fatalf("topology = %v, want only the id's host's chain", nodes)
	}
}

func containsNode(nodes []string, want string) bool {
	for _, node := range nodes {
		if node == want {
			return true
		}
	}
	return false
}

// Without a host id the address is what Python looks up, so its state is the
// one that counts.
func TestHostStateComesFromTheAddressWhenNoIDIsNamed(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1700000000, 0).UTC())
	builder.addFields([]string{"192.0.2.147|0", disabledByAddressHost, "700001", disabledByAddressHost})
	store := &Store{index: builder.index, now: builder.index.BuiltAt, maxAge: time.Hour}

	chain := admission.NewChain(
		[]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(store)},
		[]admission.Filter{admission.TargetScopeFilter{}},
	)
	facts := chain.Enrich(map[string]json.RawMessage{
		"bk_target_ip":       json.RawMessage(`"192.0.2.147"`),
		"bk_target_cloud_id": json.RawMessage(`0`),
	})
	if facts.HostState != "备用机" {
		t.Fatalf("host state = %q, want the address's host", facts.HostState)
	}
}

// The counter-example the precedence has to survive: the record names a host
// id CMDB does not know alongside an address it does know. Python looks the id
// up, gets nothing, and drops the record as an unknown host - filters.py
// branches on the dimension and never falls back to the address, so resolving
// the address here would answer "CMDB knows this host" out of a different
// host's entry and keep a series Python drops.
func TestAnUnknownHostIDIsNotRescuedByTheAddress(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1700000000, 0).UTC())
	builder.addFields([]string{"192.0.2.147|0", disabledByAddressHost, "700001", disabledByAddressHost})
	store := &Store{index: builder.index, now: builder.index.BuiltAt, maxAge: time.Hour}

	statusFilter := admission.NewHostStatusFilter([]string{"备用机"})
	chain := admission.NewChain(
		[]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(store)},
		[]admission.Filter{statusFilter},
	)
	facts := chain.Enrich(map[string]json.RawMessage{
		"bk_target_ip":       json.RawMessage(`"192.0.2.147"`),
		"bk_target_cloud_id": json.RawMessage(`0`),
		"bk_host_id":         json.RawMessage(`700009`),
	})
	if facts.HostResolved {
		t.Fatalf("facts = %+v, want the host Python looks up to stay unresolved", facts)
	}
	if facts.HostState != "" {
		t.Fatalf("host state = %q, want no state from a host Python never consulted", facts.HostState)
	}
	admitted, name, reason := chain.Admit(admission.PlanContext{}, &facts)
	if admitted || name != "host_status" || reason != "host_unknown" {
		t.Fatalf("decision = %v/%s/%s, want the unknown id rejected as Python does", admitted, name, reason)
	}

	// Target matching is the other question and it keeps both identities.
	// Python's own enrichment falls back here - TopoNodeFuller branches on the
	// host it found rather than on the dimension - so the address's topology
	// and its key have to survive the id that resolved to nothing.
	if len(facts.TopoNodes()) == 0 {
		t.Fatalf("topo nodes = %v, want the address's topology kept for target matching", facts.TopoNodes())
	}
	address := false
	for _, key := range facts.HostKeys() {
		if key == "192.0.2.147|0" {
			address = true
		}
	}
	if !address {
		t.Fatalf("host keys = %v, want the address kept for target matching", facts.HostKeys())
	}
}

// The failure a wrong CMDB coordinate produces: the cache reads fine and is
// empty. Every individual decision then looks ordinary - this host is unknown,
// this record has no topology - while together they silence every scoped
// strategy at once. An empty cache is reported as facts unavailable so the
// filters keep the alerts instead.
func TestAnEmptyHostCacheIsNotAFleetWithNoHosts(t *testing.T) {
	// Built now, so the emptiness is what degrades it rather than its age.
	builder := newIndexBuilder(time.Now())
	store := &Store{index: builder.index, now: builder.index.BuiltAt, maxAge: time.Hour}

	statusFilter := admission.NewHostStatusFilter([]string{"备用机"})
	chain := admission.NewChain(
		[]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(store)},
		[]admission.Filter{admission.TargetScopeFilter{}, statusFilter},
	)
	facts := chain.Enrich(map[string]json.RawMessage{
		"bk_target_ip":       json.RawMessage(`"192.0.2.147"`),
		"bk_target_cloud_id": json.RawMessage(`0`),
	})
	if !facts.HostFactsUnavailable {
		t.Fatalf("facts = %+v, want an empty cache reported as unavailable facts", facts)
	}
	// A topology target is exactly what an empty cache would silence.
	scope := &admission.TargetScope{Groups: []admission.TargetScopeGroup{{
		Conditions: []admission.TargetScopeCondition{{
			Field: admission.TargetScopeTopoNode, Method: admission.TargetScopeInclude,
			Keys: map[string]struct{}{"module|85": {}},
		}},
	}}}
	admitted, name, reason := chain.Admit(admission.PlanContext{TargetScope: scope}, &facts)
	if !admitted || reason != "host_facts_unavailable" {
		t.Fatalf("decision = %v/%s/%s, want the record kept and the gap named", admitted, name, reason)
	}
	if health := store.Health(); !health.Degraded || health.DegradedReason != "index_empty" {
		t.Fatalf("health = %+v, want the empty cache reported as degraded", health)
	}
}

// Whether a caller may act on the answers is its own question, and the two
// states where it may not are the two Health already names.
//
// A lookup cannot carry this. "Not held" is the right answer about one host --
// one CMDB has never heard of is not expected -- and it is the answer this
// store gives about every host when there is no index, which a caller reads as
// a target that resolved to nobody. That reading is legitimate for a real
// empty target, so nothing downstream can tell the two apart afterwards.
//
// An index past its staleness bound does not resolve either: its answers are
// facts nobody can vouch for any more, and no-data judges only on facts that
// were there (A1), as admission does since the same bound (decision-013,
// section 2 #12 and section 5.1 item 4). The bound is ten refresh intervals,
// a refresh failing for that long rather than a hiccup. At the bound itself
// the index still resolves.
func TestHostIndexResolvedFollowsWhetherThereIsAnIndexToAnswerFrom(t *testing.T) {
	clock := time.Unix(1700000000, 0).UTC()
	cold, err := NewStore(stubLoader{}, StoreOptions{
		RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if cold.HostIndexResolved() {
		t.Fatal("a store that has never loaded reported that it can resolve hosts; every lookup it " +
			"answers is 'not held', which is what a target with no hosts left looks like")
	}
	if (*Store)(nil).HostIndexResolved() {
		t.Fatal("a nil store reported that it can resolve hosts")
	}

	empty, err := NewStore(stubLoader{index: newIndexBuilder(clock).index}, StoreOptions{
		RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := empty.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if empty.HostIndexResolved() {
		t.Fatal("an index holding no host reported that it can resolve hosts; Health already calls " +
			"that state degraded because it is never a real one here")
	}

	builder := newIndexBuilder(clock)
	builder.addFields([]string{"192.0.2.141|0", multiModuleHost})
	loaded, err := NewStore(stubLoader{index: builder.index}, StoreOptions{
		RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := loaded.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !loaded.HostIndexResolved() {
		t.Fatal("an index holding hosts reported that it cannot resolve them")
	}

	// At the bound the index still resolves, through the store and through
	// the lookup the worker holds; one second past it, it does not, and
	// Health names the same state.
	lookup := NewHostBusinessLookup(loaded)
	clock = clock.Add(10 * time.Minute)
	if !loaded.HostIndexResolved() || !lookup.HostIndexResolved() {
		t.Fatal("an index exactly at its staleness bound reported that it cannot resolve hosts")
	}
	clock = clock.Add(time.Second)
	if health := loaded.Health(); !health.Degraded || health.DegradedReason != "index_stale" {
		t.Fatalf("fixture: health = %+v, want the stale state this asserts against", health)
	}
	if loaded.HostIndexResolved() || lookup.HostIndexResolved() {
		t.Fatal("an index one second past its staleness bound reported that it can resolve hosts; " +
			"no-data would judge a static target's hosts absent on facts nobody can vouch for")
	}
}
