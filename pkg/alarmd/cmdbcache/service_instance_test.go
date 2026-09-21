// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The shape the platform's writer stores for a service instance: the host it
// runs on, that host's address, and the one module chain the instance sits
// in. Instance 7 runs on the spare host, instance 8 on the live one.
const (
	instanceOnSpareHost = `{"bk_biz_id":999,"id":7,"service_instance_id":7,"name":"gateway","bk_module_id":85,
"bk_host_id":700001,"service_template_id":0,"process_instances":null,"ip":"10.0.0.7","bk_cloud_id":0,
"topo_link":{"module|85":[{"bk_obj_id":"module","bk_inst_id":85},{"bk_obj_id":"set","bk_inst_id":12},{"bk_obj_id":"biz","bk_inst_id":999}]}}`
	instanceOnLiveHost = `{"bk_biz_id":999,"id":8,"service_instance_id":8,"name":"api","bk_module_id":91,
"bk_host_id":700002,"ip":"10.0.0.8","bk_cloud_id":0,
"topo_link":{"module|91":[{"bk_obj_id":"module","bk_inst_id":91},{"bk_obj_id":"biz","bk_inst_id":999}]}}`
)

func TestAServiceInstanceRecordDecodesToItsHostAndModuleChain(t *testing.T) {
	facts, err := decodeServiceInstance(instanceOnSpareHost)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if facts.ID != "7" || facts.HostID != "700001" || facts.IP != "10.0.0.7" || facts.CloudID != "0" {
		t.Fatalf("facts = %+v", facts)
	}
	nodes := append([]string(nil), facts.TopoNodes...)
	if len(nodes) != 3 {
		t.Fatalf("topo nodes = %v, want the whole module chain", nodes)
	}
	// A record without a cloud is in the direct area, as a host without one
	// is.
	bare, err := decodeServiceInstance(`{"service_instance_id":9,"bk_host_id":1,"ip":"10.0.0.9"}`)
	if err != nil || bare.CloudID != "0" {
		t.Fatalf("bare instance = %+v, %v", bare, err)
	}
}

// A malformed instance record is skipped, the field is the lookup key, and
// the count is of instances the index can answer about.
func TestTheServiceInstanceIndexIsBuiltFromTheHashFields(t *testing.T) {
	builder := newIndexBuilder(time.Unix(1700000000, 0).UTC())
	builder.addServiceInstanceFields([]string{"7", instanceOnSpareHost, "broken", "{not json", "8", instanceOnLiveHost})
	index := builder.index
	if index.ServiceInstances() != 2 {
		t.Fatalf("instances = %d, want the two readable ones", index.ServiceInstances())
	}
	if instance, found := index.LookupServiceInstance("7"); !found || instance.HostID != "700001" {
		t.Fatalf("instance 7 = %+v, %v", instance, found)
	}
	if _, found := index.LookupServiceInstance("9"); found {
		t.Fatal("an unknown instance resolved")
	}
	if _, found := (*Index)(nil).LookupServiceInstance("7"); found {
		t.Fatal("a nil index resolved an instance")
	}
}

// hashClient serves the two hashes Load scans, so the reader is exercised
// against the keys it derives rather than against a builder fed by hand.
type hashClient struct {
	redis.Cmdable
	hashes map[string][]string
	scans  []string
}

func (client *hashClient) HScan(_ context.Context, key string, _ uint64, _ string, _ int64) *redis.ScanCmd {
	client.scans = append(client.scans, key)
	return redis.NewScanCmdResult(client.hashes[key], 0, nil)
}

func (client *hashClient) Get(_ context.Context, _ string) *redis.StringCmd {
	return redis.NewStringResult("", redis.Nil)
}

// Both caches hang off the platform prefix and are read into one snapshot:
// an instance resolved against a host index from another refresh would sit
// under a module the host has since left.
func TestLoadReadsHostsAndServiceInstancesIntoOneSnapshot(t *testing.T) {
	client := &hashClient{hashes: map[string][]string{
		"bk_monitorv3.ce.cache.cmdb.host":             {"10.0.0.7|0", disabledByAddressHost, "700001", disabledByAddressHost},
		"bk_monitorv3.ce.cache.cmdb.service_instance": {"7", instanceOnSpareHost},
	}}
	reader, err := NewReader(client, "bk_monitorv3.ce")
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	index, err := reader.Load(context.Background(), time.Unix(1700000000, 0).UTC())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if index.Hosts() != 1 || index.ServiceInstances() != 1 {
		t.Fatalf("index holds %d hosts and %d instances", index.Hosts(), index.ServiceInstances())
	}
	if !reflect.DeepEqual(client.scans, []string{"bk_monitorv3.ce.cache.cmdb.host", "bk_monitorv3.ce.cache.cmdb.service_instance"}) {
		t.Fatalf("scanned %v", client.scans)
	}
	store := &Store{index: index, now: time.Now, maxAge: time.Hour, interval: time.Minute}
	if health := store.Health(); health.Hosts != 1 || health.ServiceInstances != 1 {
		t.Fatalf("health = %+v", health)
	}
}

func instanceChain(t *testing.T, store *Store, disabledStates ...string) *admission.Chain {
	t.Helper()
	filters := []admission.Filter{admission.TargetScopeFilter{}}
	if len(disabledStates) > 0 {
		statusFilter, installed := admission.NewHostStatusFilter(disabledStates)
		if !installed {
			t.Fatal("NewHostStatusFilter declined to install")
		}
		filters = append(filters, statusFilter)
	}
	return admission.NewChain(
		[]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(store), NewServiceInstanceTopologyFuller(store)},
		filters,
	)
}

func storeWith(hostFields []string, instanceFields []string) *Store {
	builtAt := time.Unix(1700000000, 0).UTC()
	builder := newIndexBuilder(builtAt)
	builder.addFields(hostFields)
	builder.addServiceInstanceFields(instanceFields)
	return &Store{index: builder.index, now: func() time.Time { return builtAt }, maxAge: time.Hour, interval: time.Minute}
}

// A series that names only a service instance is placed under the instance's
// module chain, learns the host the instance runs on, and is judged by that
// host's state - the effect of Python writing bk_topo_node, bk_target_ip and
// bk_target_cloud_id into the record and its host status filter reading
// them back. The dimensions are not written here.
func TestAServiceInstanceSeriesIsPlacedUnderItsModuleAndJudgedByItsHost(t *testing.T) {
	store := storeWith(
		[]string{"10.0.0.7|0", disabledByAddressHost, "700001", disabledByAddressHost, "10.0.0.8|0", monitoredByIDHost, "700002", monitoredByIDHost},
		[]string{"7", instanceOnSpareHost, "8", instanceOnLiveHost},
	)
	chain := instanceChain(t, store, "备用机")
	dimensions := map[string]json.RawMessage{"bk_target_service_instance_id": json.RawMessage(`7`)}
	facts := chain.Enrich(dimensions)

	if got := facts.TopoNodes(); !reflect.DeepEqual(got, []string{"biz|999", "module|85", "set|12"}) {
		t.Fatalf("topo nodes = %v, want the instance's module chain", got)
	}
	// By address only: Python's instance branch writes bk_target_ip and
	// bk_target_cloud_id and never bk_host_id, so the instance's own host id
	// is not a key the record can be matched by.
	if got := facts.HostKeys(); !reflect.DeepEqual(got, []string{"10.0.0.7|0"}) {
		t.Fatalf("host keys = %v, want the instance's host by address only", got)
	}
	if !facts.HostResolved || facts.HostState != "备用机" || facts.HostBusinessID != "999" {
		t.Fatalf("facts = %+v, want the instance's host resolved", facts)
	}
	naming := facts.HostNaming
	if !naming.NamedAddress || !naming.NamedCloud || !naming.Usable || naming.AddressKey != "10.0.0.7|0" || naming.IDKey != "" {
		t.Fatalf("naming = %+v, want the record named by the instance's address", naming)
	}
	if len(dimensions) != 1 {
		t.Fatalf("enrichment wrote into the dimensions: %v", dimensions)
	}

	// A service topology target on the instance's module admits it.
	moduleTarget := admission.PlanContext{TargetScope: &admission.TargetScope{Groups: []admission.TargetScopeGroup{{
		Conditions: []admission.TargetScopeCondition{{
			Field: admission.TargetScopeTopoNode, Method: admission.TargetScopeInclude,
			Keys: map[string]struct{}{"module|85": {}},
		}},
	}}}}
	if decision := (admission.TargetScopeFilter{}).Admit(moduleTarget, &facts); !decision.Admit {
		t.Fatalf("a target on the instance's module refused it: %+v", decision)
	}
	// And the host status filter drops it, because the host it runs on is a
	// spare - the effect Python reaches by looking up the address it wrote.
	admitted, name, reason := chain.Admit(moduleTarget, &facts)
	if admitted || name != "host_status" || reason != "monitoring_disabled" {
		t.Fatalf("decision = %v/%s/%s, want the instance's host state applied", admitted, name, reason)
	}

	// The same target, an instance on the live host: admitted end to end.
	live := chain.Enrich(map[string]json.RawMessage{"service_instance_id": json.RawMessage(`"8"`)})
	liveTarget := admission.PlanContext{TargetScope: &admission.TargetScope{Groups: []admission.TargetScopeGroup{{
		Conditions: []admission.TargetScopeCondition{{
			Field: admission.TargetScopeTopoNode, Method: admission.TargetScopeInclude,
			Keys: map[string]struct{}{"module|91": {}},
		}},
	}}}}
	if admitted, name, reason := chain.Admit(liveTarget, &live); !admitted {
		t.Fatalf("an instance on a live host was refused: %s/%s", name, reason)
	}

	// A host target against an instance-only series is decided by the
	// instance's address and by nothing else, in both directions. Python's
	// is_match builds the record's keys from bk_host_id (which the instance
	// branch never wrote) and bk_target_ip|bk_target_cloud_id (which it did);
	// a target frozen as the host's id alone therefore does not name this
	// series, and an exclusion frozen the same way does not drop it. The
	// instance's own bk_host_id counting as a key would flip both.
	hostTarget := func(method admission.TargetScopeMethod, keys ...string) admission.PlanContext {
		set := make(map[string]struct{}, len(keys))
		for _, key := range keys {
			set[key] = struct{}{}
		}
		return admission.PlanContext{TargetScope: &admission.TargetScope{Groups: []admission.TargetScopeGroup{{
			Conditions: []admission.TargetScopeCondition{{Field: admission.TargetScopeHost, Method: method, Keys: set}},
		}}}}
	}
	scope := admission.TargetScopeFilter{}
	if decision := scope.Admit(hostTarget(admission.TargetScopeInclude, "700002"), &live); decision.Admit {
		t.Fatalf("a host target frozen as the instance's host id alone admitted an instance-only series: %+v", decision)
	}
	if decision := scope.Admit(hostTarget(admission.TargetScopeExclude, "700002"), &live); !decision.Admit {
		t.Fatalf("a host exclusion frozen as the instance's host id alone dropped an instance-only series: %+v", decision)
	}
	if decision := scope.Admit(hostTarget(admission.TargetScopeInclude, "700002", "10.0.0.8|0"), &live); !decision.Admit {
		t.Fatalf("a host target frozen with the address refused the instance on it: %+v", decision)
	}
	if decision := scope.Admit(hostTarget(admission.TargetScopeExclude, "700002", "10.0.0.8|0"), &live); decision.Admit {
		t.Fatalf("a host exclusion frozen with the address admitted the instance on it: %+v", decision)
	}
}

// Python's precedence, transcribed: a record that resolved its host by id
// never reaches the instance branch, so the host's own topology stands; a
// record naming an address and an instance takes the instance's chain over
// the address's, because Python assigns bk_topo_node from the instance.
func TestTheInstanceIsConsultedOnlyWhereTheHostByIDWasNot(t *testing.T) {
	store := storeWith(
		[]string{"10.0.0.7|0", disabledByAddressHost, "700001", disabledByAddressHost, "10.0.0.8|0", monitoredByIDHost, "700002", monitoredByIDHost},
		[]string{"7", instanceOnSpareHost},
	)
	chain := instanceChain(t, store)

	byID := chain.Enrich(map[string]json.RawMessage{
		"bk_host_id": json.RawMessage(`700002`), "bk_target_service_instance_id": json.RawMessage(`7`),
	})
	if got := byID.TopoNodes(); !reflect.DeepEqual(got, []string{"biz|999", "module|91"}) {
		t.Fatalf("topo nodes = %v, want the host's own chain when the id resolved", got)
	}
	if byID.HostState != "运营中[需告警]" {
		t.Fatalf("host state = %q, want the id's host", byID.HostState)
	}

	byAddress := chain.Enrich(map[string]json.RawMessage{
		"bk_target_ip": json.RawMessage(`"10.0.0.8"`), "bk_target_cloud_id": json.RawMessage(`0`),
		"bk_target_service_instance_id": json.RawMessage(`7`),
	})
	if got := byAddress.TopoNodes(); !reflect.DeepEqual(got, []string{"biz|999", "module|85", "set|12"}) {
		t.Fatalf("topo nodes = %v, want the instance's chain over the address's", got)
	}
	if got := byAddress.HostKeys(); !reflect.DeepEqual(got, []string{"10.0.0.7|0"}) {
		t.Fatalf("host keys = %v, want the instance's address, not the address the record arrived with and not the instance's host id", got)
	}
	if byAddress.HostState != "备用机" {
		t.Fatalf("host state = %q, want the instance's host", byAddress.HostState)
	}

	// An id CMDB does not know, beside a known instance: the topology comes
	// from the instance, but the host status filter still reads the id first
	// and drops the record as unknown, as Python's does.
	unknownID := chain.Enrich(map[string]json.RawMessage{
		"bk_host_id": json.RawMessage(`700009`), "bk_target_service_instance_id": json.RawMessage(`7`),
	})
	if got := unknownID.TopoNodes(); !reflect.DeepEqual(got, []string{"biz|999", "module|85", "set|12"}) {
		t.Fatalf("topo nodes = %v, want the instance's chain", got)
	}
	if unknownID.HostResolved || unknownID.HostNaming.IDKey != "700009" {
		t.Fatalf("facts = %+v, want the unknown id kept as the host Python looks up", unknownID)
	}
	if got := unknownID.HostKeys(); !reflect.DeepEqual(got, []string{"10.0.0.7|0", "700009"}) {
		t.Fatalf("host keys = %v, want the record's id kept beside the instance's address", got)
	}
	judged := instanceChain(t, store, "备用机")
	if admitted, name, reason := judged.Admit(admission.PlanContext{}, &unknownID); admitted || name != "host_status" || reason != "host_unknown" {
		t.Fatalf("decision = %v/%s/%s, want the unknown id rejected as Python does", admitted, name, reason)
	}
}

// An instance the cache does not hold leaves the record as the host fuller
// left it, and a record that names no instance is not touched at all.
func TestAnUnknownInstanceLeavesTheRecordAsItWas(t *testing.T) {
	store := storeWith(
		[]string{"10.0.0.8|0", monitoredByIDHost, "700002", monitoredByIDHost},
		[]string{"7", instanceOnSpareHost},
	)
	chain := instanceChain(t, store)
	facts := chain.Enrich(map[string]json.RawMessage{
		"bk_target_ip": json.RawMessage(`"10.0.0.8"`), "bk_target_cloud_id": json.RawMessage(`0`),
		"bk_target_service_instance_id": json.RawMessage(`99`),
	})
	if got := facts.TopoNodes(); !reflect.DeepEqual(got, []string{"biz|999", "module|91"}) {
		t.Fatalf("topo nodes = %v, want the address's topology kept", got)
	}
	if facts.HostState != "运营中[需告警]" || facts.HostFactsUnavailable {
		t.Fatalf("facts = %+v", facts)
	}
	hostOnly := chain.Enrich(map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"10.0.0.8"`)})
	if hostOnly.HostFactsUnavailable || len(hostOnly.ServiceInstanceKeys()) != 0 {
		t.Fatalf("a record naming no instance was touched: %+v", hostOnly)
	}
}

// An instance cache with nothing in it, read by a series that names an
// instance, is a cache nobody writes - the same reading as an empty host
// cache, named apart from it because a different job writes each. The
// record is admitted and the gap is counted under its own reason; a record
// that names no instance is unaffected.
func TestAnEmptyInstanceCacheIsNamedApartFromAnEmptyHostCache(t *testing.T) {
	store := storeWith([]string{"10.0.0.8|0", monitoredByIDHost, "700002", monitoredByIDHost}, nil)
	chain := instanceChain(t, store, "备用机")
	facts := chain.Enrich(map[string]json.RawMessage{"bk_target_service_instance_id": json.RawMessage(`7`)})
	if !facts.HostFactsUnavailable || facts.FactsUnavailableReason() != admission.FactsUnavailableServiceInstanceIndex {
		t.Fatalf("facts = %+v, want the instance index reported unavailable", facts)
	}
	scope := &admission.TargetScope{Groups: []admission.TargetScopeGroup{{
		Conditions: []admission.TargetScopeCondition{{
			Field: admission.TargetScopeTopoNode, Method: admission.TargetScopeInclude,
			Keys: map[string]struct{}{"module|85": {}},
		}},
	}}}
	admitted, name, reason := chain.Admit(admission.PlanContext{TargetScope: scope}, &facts)
	if !admitted || name != "target_scope" || reason != "service_instance_facts_unavailable" {
		t.Fatalf("decision = %v/%s/%s, want the record kept and the instance gap named", admitted, name, reason)
	}
	// The store itself is not degraded: a fleet without instances is real.
	if health := store.Health(); health.Degraded || health.ServiceInstances != 0 {
		t.Fatalf("health = %+v", health)
	}
	hostSeries := chain.Enrich(map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"10.0.0.8"`), "bk_target_cloud_id": json.RawMessage(`0`)})
	if hostSeries.HostFactsUnavailable {
		t.Fatalf("a host series was affected by the empty instance cache: %+v", hostSeries)
	}
	// With the host cache empty too, the host index is the first to fail
	// and names the reason.
	empty := storeWith(nil, nil)
	both := instanceChain(t, empty).Enrich(map[string]json.RawMessage{"bk_target_service_instance_id": json.RawMessage(`7`)})
	if both.FactsUnavailableReason() != admission.FactsUnavailableHostIndex {
		t.Fatalf("reason = %q, want the host index named first", both.FactsUnavailableReason())
	}
}

// The scalar fields of the resolved host are readable as host attributes
// without any of them being copied per series; nested fields are not.
func TestTheResolvedHostsScalarFieldsAreExposedAsAttributes(t *testing.T) {
	store := storeWith([]string{"10.0.0.8|0", monitoredByIDHost, "700002", monitoredByIDHost}, nil)
	chain := instanceChain(t, store)
	facts := chain.Enrich(map[string]json.RawMessage{"bk_host_id": json.RawMessage(`700002`)})
	for attribute, want := range map[string]string{
		"bk_state": "运营中[需告警]", "display_name": "live", "bk_host_id": "700002", "bk_biz_id": "999", "bk_cloud_id": "0",
	} {
		if got := facts.Candidates(contract.AttributeHostPrefix + attribute); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("host attribute %s = %v, want %q", attribute, got, want)
		}
	}
	if got := facts.Candidates(contract.AttributeHostPrefix + "topo_link"); got != nil {
		t.Fatalf("a nested field was flattened into an attribute: %v", got)
	}
	if _, copied := facts.Attributes[contract.AttributeHostPrefix+"bk_state"]; copied {
		t.Fatal("host attributes were copied into the per-series map")
	}
}

// The separation the whole design rests on: what enrichment learns from CMDB
// changes when CMDB changes, and none of it may reach the series the alert
// is identified by. The same record is enriched against three snapshots -
// the host moved to another module, the instance moved to another host, the
// host's attributes changed - and the fingerprint and the dimensions are
// byte for byte what they were, while the facts did change.
func TestCMDBChangesNeverReachTheFingerprintOrTheDimensions(t *testing.T) {
	dimensions := map[string]json.RawMessage{
		"bk_target_ip": json.RawMessage(`"10.0.0.7"`), "bk_target_cloud_id": json.RawMessage(`0`),
		"bk_target_service_instance_id": json.RawMessage(`7`), "device": json.RawMessage(`"eth0"`),
	}
	identity := contract.MonitorOutputIdentity{DimensionFields: []string{"bk_target_ip", "bk_target_cloud_id", "bk_target_service_instance_id", "device"}}
	fingerprint := func() string {
		t.Helper()
		digest, err := contract.MonitorDedupeMD5("77", "999", dimensions, identity)
		if err != nil {
			t.Fatalf("fingerprint: %v", err)
		}
		return digest
	}
	encoded := func() string {
		raw, err := json.Marshal(dimensions)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		return string(raw)
	}
	wantFingerprint, wantDimensions := fingerprint(), encoded()

	// The writer derives an instance's chain from its host's, so a host that
	// moved modules moves its instance's chain with it.
	moduleMove := func(document string) string {
		return strings.Replace(document, `"module|85":[{"bk_obj_id":"module","bk_inst_id":85}`, `"module|86":[{"bk_obj_id":"module","bk_inst_id":86}`, 1)
	}
	movedHost, movedHostInstance := moduleMove(disabledByAddressHost), moduleMove(instanceOnSpareHost)
	movedInstance := strings.Replace(strings.Replace(instanceOnSpareHost, `"bk_host_id":700001`, `"bk_host_id":700002`, 1), `"ip":"10.0.0.7"`, `"ip":"10.0.0.8"`, 1)
	changedHost := strings.Replace(disabledByAddressHost, `"bk_state":"备用机"`, `"bk_state":"运营中[需告警]"`, 1)
	snapshots := map[string]*Store{
		"baseline":                storeWith([]string{"10.0.0.7|0", disabledByAddressHost, "700001", disabledByAddressHost}, []string{"7", instanceOnSpareHost}),
		"host moved modules":      storeWith([]string{"10.0.0.7|0", movedHost, "700001", movedHost}, []string{"7", movedHostInstance}),
		"instance moved hosts":    storeWith([]string{"10.0.0.7|0", disabledByAddressHost, "700001", disabledByAddressHost, "10.0.0.8|0", monitoredByIDHost, "700002", monitoredByIDHost}, []string{"7", movedInstance}),
		"host attributes changed": storeWith([]string{"10.0.0.7|0", changedHost, "700001", changedHost}, []string{"7", instanceOnSpareHost}),
	}
	seen := make(map[string]string, len(snapshots))
	for name, store := range snapshots {
		facts := instanceChain(t, store).Enrich(dimensions)
		if got := fingerprint(); got != wantFingerprint {
			t.Fatalf("%s: fingerprint changed from %s to %s", name, wantFingerprint, got)
		}
		if got := encoded(); got != wantDimensions {
			t.Fatalf("%s: dimensions changed from %s to %s", name, wantDimensions, got)
		}
		seen[name] = strings.Join(facts.TopoNodes(), ",") + ";" + strings.Join(facts.HostKeys(), ",") + ";" + facts.HostState
	}
	// The facts did follow CMDB - otherwise the invariance above proves
	// nothing about separation, only that nothing happened.
	for name, snapshot := range seen {
		if name != "baseline" && snapshot == seen["baseline"] {
			t.Fatalf("%s produced the same facts as the baseline: %s", name, snapshot)
		}
	}
}
