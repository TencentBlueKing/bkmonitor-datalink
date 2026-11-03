// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

// The host a record is about, decided the way Python decides it, through the
// real fullers and filters. Expected values are read from bk-monitor
// c0e828fa8c, not from this package:
//
//   - the fuller (alarm_backends/service/access/data/fullers.py:55-110) finds
//     the host by bk_host_id when the record carries a true one, else by
//     service instance, else by address - bk_target_ip or ip, in
//     bk_target_cloud_id or bk_cloud_id or "0" - and writes into the record
//     the host it found: by id, that host's address and topology; by address,
//     the cloud it looked in, the topology, and the host's id when the record
//     has no bk_host_id dimension;
//   - the host status filter (filters.py:85-120) then reads the record as the
//     fuller left it;
//   - the target match (bkmonitor/utils/range/target.py:112-120) reads the
//     same record, by presence: data.get("bk_target_ip", data.get("ip")).

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// 700001 is 192.0.2.147 in cloud 0, spare (disabled), in module 85; 700002 is
// 192.0.2.148 in cloud 0, monitored, in module 91.
func pythonNamingStore() *Store {
	return storeWith(
		[]string{"192.0.2.147|0", disabledByAddressHost, "700001", disabledByAddressHost,
			"192.0.2.148|0", monitoredByIDHost, "700002", monitoredByIDHost},
		nil,
	)
}

func dims(pairs ...string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for i := 0; i+1 < len(pairs); i += 2 {
		out[pairs[i]] = json.RawMessage(pairs[i+1])
	}
	return out
}

func scopeOf(field admission.TargetScopeField, method admission.TargetScopeMethod, keys ...string) admission.PlanContext {
	set := map[string]struct{}{}
	for _, k := range keys {
		set[k] = struct{}{}
	}
	return admission.PlanContext{TargetScope: &admission.TargetScope{Groups: []admission.TargetScopeGroup{{
		Conditions: []admission.TargetScopeCondition{{Field: field, Method: method, Keys: set}}}}}}
}

// The fuller finds 192.0.2.147 by ip, or by bk_target_ip in the default cloud,
// and writes its id 700001; the filter looks 700001 up and drops the spare
// host's record. A record that spelled both bk_target_ip and
// bk_target_cloud_id was always dropped and is the control.
func TestADisabledHostNamedByAliasOrWithoutItsCloudIsDropped(t *testing.T) {
	chain := instanceChain(t, pythonNamingStore(), "备用机")
	for name, record := range map[string]map[string]json.RawMessage{
		"ip and bk_cloud_id only":          dims("ip", `"192.0.2.147"`, "bk_cloud_id", `0`),
		"ip only":                          dims("ip", `"192.0.2.147"`),
		"bk_target_ip without cloud":       dims("bk_target_ip", `"192.0.2.147"`),
		"bk_target_ip with bk_cloud_id":    dims("bk_target_ip", `"192.0.2.147"`, "bk_cloud_id", `0`),
		"bk_target_ip with an empty cloud": dims("bk_target_ip", `"192.0.2.147"`, "bk_target_cloud_id", `""`),
		"control: bk_target_ip and cloud":  dims("bk_target_ip", `"192.0.2.147"`, "bk_target_cloud_id", `"0"`),
	} {
		facts := chain.Enrich(record)
		admit, filter, reason := chain.Admit(admission.PlanContext{}, &facts)
		if admit || reason != "monitoring_disabled" {
			t.Errorf("%s: admit=%v by %s/%s; Python drops the spare host's record for its state", name, admit, filter, reason)
		}
	}
}

// The true half of the 09-10 fix stays: an alias whose host CMDB does not
// know makes the fuller write nothing, so the filter does not see host data.
func TestAnAliasWhoseHostIsUnknownIsLeftAlone(t *testing.T) {
	chain := instanceChain(t, pythonNamingStore(), "备用机")
	facts := chain.Enrich(dims("ip", `"192.0.2.199"`, "bk_cloud_id", `0`))
	if admit, filter, reason := chain.Admit(admission.PlanContext{}, &facts); !admit {
		t.Errorf("dropped by %s/%s; Python keeps a record whose alias names no known host", filter, reason)
	}
}

// The other direction of the same seam: an empty bk_target_ip with an ip of
// a monitored host. The fuller reads `bk_target_ip or ip`, finds 700002 and
// writes its id; the filter sees a usable id and keeps the record
// (filters.py:96-98 no longer applies). Had the alias not resolved, the
// record would be invalid host data and dropped.
func TestAnEmptyTargetAddressWithAResolvableAliasIsKept(t *testing.T) {
	chain := instanceChain(t, pythonNamingStore(), "备用机")
	facts := chain.Enrich(dims("bk_target_ip", `""`, "ip", `"192.0.2.148"`))
	if admit, filter, reason := chain.Admit(admission.PlanContext{}, &facts); !admit {
		t.Errorf("dropped by %s/%s; Python keeps the monitored host it resolved by the alias", filter, reason)
	}
	// target.py reads bk_target_ip by presence: it is there and empty, so
	// the only key is the written id.
	if keys := facts.HostKeys(); len(keys) != 1 || keys[0] != "700002" {
		t.Errorf("target keys = %v, want only the written id", keys)
	}
	unresolved := chain.Enrich(dims("bk_target_ip", `""`, "ip", `"192.0.2.199"`))
	if admit, _, reason := chain.Admit(admission.PlanContext{}, &unresolved); admit || reason != "host_identity_invalid" {
		t.Errorf("unresolvable alias with an empty bk_target_ip: admit=%v reason=%s, want host_identity_invalid", admit, reason)
	}
}

// The clouds are read by truthiness for the lookup and by presence for the
// target key, and the written cloud joins the two: found in the default cloud,
// the key is "|0"; found through bk_cloud_id 5 when bk_target_cloud_id is
// empty, the key is "|5". The host here is in cloud 5.
func TestTheCloudTheFullerFoundTheHostInIsTheTargetKeysCloud(t *testing.T) {
	inCloudFive := `{"bk_host_id":700005,"bk_host_innerip":"192.0.2.149","bk_cloud_id":5,"bk_biz_id":999,
"bk_state":"运营中[需告警]","display_name":"five","topo_link":{"module|95":[{"bk_obj_id":"module","bk_inst_id":95}]}}`
	store := storeWith([]string{"192.0.2.148|0", monitoredByIDHost, "700002", monitoredByIDHost,
		"192.0.2.149|5", inCloudFive, "700005", inCloudFive}, nil)
	chain := instanceChain(t, store, "备用机")
	for name, c := range map[string]struct {
		record map[string]json.RawMessage
		keys   []string
	}{
		"empty target cloud, found in the default": {dims("bk_target_ip", `"192.0.2.148"`, "bk_target_cloud_id", `""`), []string{"700002", "192.0.2.148|0"}},
		"empty target cloud, found in bk_cloud_id": {dims("bk_target_ip", `"192.0.2.149"`, "bk_target_cloud_id", `""`, "bk_cloud_id", `5`), []string{"700005", "192.0.2.149|5"}},
	} {
		facts := chain.Enrich(c.record)
		got := map[string]bool{}
		for _, key := range facts.HostKeys() {
			got[key] = true
		}
		if len(got) != len(c.keys) || !got[c.keys[0]] || !got[c.keys[1]] {
			t.Errorf("%s: target keys = %v, want %v", name, facts.HostKeys(), c.keys)
		}
	}
}

// A bk_host_id that is present but empty is no id, so the fuller looks the
// host up by address - and, the dimension being present, does not write the
// found host's id. The filter then has no id value, and looks the host up by
// bk_target_ip and the written cloud instead: the spare host, dropped.
func TestAPresentButEmptyHostIDIsNotOverwritten(t *testing.T) {
	chain := instanceChain(t, pythonNamingStore(), "备用机")
	facts := chain.Enrich(dims("bk_host_id", `""`, "bk_target_ip", `"192.0.2.147"`))
	if facts.HostNaming.IDKey != "" {
		t.Errorf("id = %q, want none written over a present dimension", facts.HostNaming.IDKey)
	}
	if admit, _, reason := chain.Admit(admission.PlanContext{}, &facts); admit || reason != "monitoring_disabled" {
		t.Errorf("admit=%v reason=%s, want the spare host found by address and dropped", admit, reason)
	}
}

// A host id CMDB does not know stays the id the filter looks up (the drop is
// TestAnUnknownHostIDIsNotRescuedByTheAddress). Python still goes on to the
// address branch and takes the topology from the host there (fullers.py:
// 92-108), so a topology target sees that chain.
func TestAnUnknownHostIDTakesItsTopologyFromTheAddress(t *testing.T) {
	chain := instanceChain(t, pythonNamingStore(), "备用机")
	facts := chain.Enrich(dims("bk_host_id", `"800001"`, "bk_target_ip", `"192.0.2.148"`, "bk_target_cloud_id", `"0"`))
	if admit, _, reason := chain.Admit(admission.PlanContext{}, &facts); admit || reason != "host_unknown" {
		t.Errorf("admit=%v reason=%s, want host_unknown", admit, reason)
	}
	if !containsNode(facts.TopoNodes(), "module|91") {
		t.Errorf("topology = %v, want the address's host's chain", facts.TopoNodes())
	}
}

// Found by id, the host's own address replaces the record's and its topology
// is the only one (fullers.py:61-74): the record's stale address no longer
// puts it in the other host's scope, either way round.
func TestAHostFoundByIDReplacesTheRecordsAddress(t *testing.T) {
	chain := instanceChain(t, pythonNamingStore())
	record := dims("bk_host_id", `"700002"`, "bk_target_ip", `"192.0.2.147"`, "bk_target_cloud_id", `"0"`)
	for _, c := range []struct {
		name string
		plan admission.PlanContext
		want bool
	}{
		{"topo eq the other host's module", scopeOf(admission.TargetScopeTopoNode, admission.TargetScopeInclude, "module|85"), false},
		{"topo eq the id's host's module", scopeOf(admission.TargetScopeTopoNode, admission.TargetScopeInclude, "module|91"), true},
		{"host eq the record's stale address", scopeOf(admission.TargetScopeHost, admission.TargetScopeInclude, "192.0.2.147|0"), false},
		{"host neq the record's stale address", scopeOf(admission.TargetScopeHost, admission.TargetScopeExclude, "192.0.2.147|0"), true},
		{"host eq the id's host's address", scopeOf(admission.TargetScopeHost, admission.TargetScopeInclude, "192.0.2.148|0"), true},
	} {
		facts := chain.Enrich(record)
		admit, filter, reason := chain.Admit(c.plan, &facts)
		if admit != c.want {
			t.Errorf("%s: admit=%v (%s/%s), Python=%v; keys=%v topo=%v", c.name, admit, filter, reason, c.want, facts.HostKeys(), facts.TopoNodes())
		}
	}
}

type staticMembers map[string]struct{}

func (m staticMembers) Contains(key string) bool { _, ok := m[key]; return ok }

// The host_id rule's key is the record's bk_host_id, and the id the cache
// teaches only a record that names its host by address (decision-017
// section 2.3 and the 09-21 host model exemption): a record carrying 700002
// is not admitted into a plan of 700001 because its address is 700001's.
func TestATargetPlanHostIdentityIsOneHostNotAUnion(t *testing.T) {
	chain := admission.NewChain(
		[]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(pythonNamingStore())},
		[]admission.Filter{admission.TargetPlanFilter{}})
	plan := admission.PlanContext{TargetPlan: &admission.TargetPlanContext{
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true},
		Members:  staticMembers{"700001": {}}}}
	carried := chain.Enrich(dims("bk_host_id", `"700002"`, "bk_target_ip", `"192.0.2.147"`, "bk_target_cloud_id", `"0"`))
	if admit, _, _ := chain.Admit(plan, &carried); admit {
		t.Errorf("admitted host 700002 into a plan of 700001; keys %v", carried.HostKeys())
	}
	taught := chain.Enrich(dims("bk_target_ip", `"192.0.2.147"`, "bk_target_cloud_id", `"0"`))
	if admit, filter, reason := chain.Admit(plan, &taught); !admit {
		t.Errorf("a record naming 700001 by address: rejected by %s/%s, want admitted by the taught id", filter, reason)
	}
}

// Python has two host state lists acting at different stages. The disabled
// monitoring states (备用机/测试中/故障中, HOST_DISABLE_MONITOR_STATES) make
// host.ignore_monitoring true and drop the record at access (filters.py:118),
// matched as `state in host.bk_state` - a substring. The no-notice states
// (运营中[无告警]/开发中[无告警], HOST_DISABLE_NOTICE_STATES) make
// host.is_shielding true, which only the converge shield reads
// (converge/shield/shielder/saas_config.py:293): detection still runs and the
// alert exists, shielded - the alarm center's work, not alarmd's. So a
// no-notice host's series is admitted, and a disabled one's is dropped
// however it is named, its state string carrying more than the word included.
func TestOnlyTheDisabledMonitoringStatesDropAHostsSeries(t *testing.T) {
	host := func(id int, ip, state string) string {
		return `{"bk_host_id":` + strconv.Itoa(id) + `,"bk_host_innerip":"` + ip + `","bk_cloud_id":0,"bk_biz_id":999,"bk_state":"` +
			state + `","display_name":"h","topo_link":{"module|1":[{"bk_obj_id":"module","bk_inst_id":1}]}}`
	}
	noNotice, suffixed := host(710001, "192.0.2.161", "运营中[无告警]"), host(710002, "192.0.2.162", "备用机-待回收")
	store := storeWith([]string{"710001", noNotice, "192.0.2.161|0", noNotice, "710002", suffixed, "192.0.2.162|0", suffixed}, nil)
	chain := instanceChain(t, store, "备用机", "测试中", "故障中")
	for _, c := range []struct {
		name   string
		record map[string]json.RawMessage
		admit  bool
	}{
		{"a no-notice host by id", dims("bk_host_id", `"710001"`), true},
		{"a no-notice host by alias", dims("ip", `"192.0.2.161"`, "bk_cloud_id", `0`), true},
		{"a spare host whose state carries a suffix, by id", dims("bk_host_id", `"710002"`), false},
		{"the same host by alias", dims("ip", `"192.0.2.162"`), false},
	} {
		facts := chain.Enrich(c.record)
		if admit, filter, reason := chain.Admit(admission.PlanContext{}, &facts); admit != c.admit {
			t.Errorf("%s: admit=%v (%s/%s), want %v", c.name, admit, filter, reason, c.admit)
		}
	}
}
