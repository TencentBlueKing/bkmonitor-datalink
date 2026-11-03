// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

// Python's fuller looks a host up by bk_agent_id when the record carries no
// true bk_host_id (bk-monitor c0e828fa8c, fullers.py:57-74), through the agent
// hash, and only accepts a host whose own record carries that agent id
// (core/cache/cmdb/host.py:207-221). Found, it is treated as found by id.

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
)

func agentStore(agents map[string]string) *Store {
	return agentStoreWithInstances(agents, nil)
}

// agentStoreWithInstances is agentStore with service instances beside the
// hosts.
func agentStoreWithInstances(agents map[string]string, instances []string) *Store {
	spare := `{"bk_host_id":720001,"bk_host_innerip":"192.0.2.171","bk_cloud_id":0,"bk_biz_id":999,"bk_agent_id":"agent-spare",
"bk_state":"备用机","display_name":"s","topo_link":{"module|85":[{"bk_obj_id":"module","bk_inst_id":85}]}}`
	live := `{"bk_host_id":720002,"bk_host_innerip":"192.0.2.172","bk_cloud_id":0,"bk_biz_id":999,"bk_agent_id":"agent-live",
"bk_state":"运营中[需告警]","display_name":"l","topo_link":{"module|91":[{"bk_obj_id":"module","bk_inst_id":91}]}}`
	store := storeWith([]string{"720001", spare, "192.0.2.171|0", spare, "720002", live, "192.0.2.172|0", live}, instances)
	store.index.byAgent = agents
	return store
}

func TestAHostNamedByItsAgentIsPlacedAsPythonPlacesIt(t *testing.T) {
	store := agentStore(map[string]string{"agent-spare": "720001", "agent-live": "720002", "agent-moved": "720002"})
	chain := instanceChain(t, store, "备用机")
	hostEq720001 := scopeOf(admission.TargetScopeHost, admission.TargetScopeInclude, "720001", "192.0.2.171|0")
	for _, c := range []struct {
		name   string
		plan   admission.PlanContext
		record string
		admit  bool
		reason string
	}{
		// Placed: the target is matched on the agent's host, which is not
		// the target's host here, and its state is the one judged.
		{"agent of another host than the target's", hostEq720001, `{"bk_agent_id":"agent-live"}`, false, "out_of_scope"},
		{"agent of the target's host, in a disabled state", admission.PlanContext{}, `{"bk_agent_id":"agent-spare"}`, false, "monitoring_disabled"},
		{"agent of a monitored host, no target", admission.PlanContext{}, `{"bk_agent_id":"agent-live"}`, true, ""},
		// Not placed: the hash points at a host whose own record carries
		// another agent, so Python finds nothing and the host condition has
		// no key to judge - skipped, as for a record with no host.
		{"agent the host does not carry", hostEq720001, `{"bk_agent_id":"agent-moved"}`, true, ""},
		// A true bk_host_id takes precedence: the agent is not consulted.
		// The id is the monitored host's and the agent the spare's; placed by
		// the agent, the record would be dropped for the spare's state.
		{"a true id beside the agent", admission.PlanContext{}, `{"bk_host_id":"720002","bk_agent_id":"agent-spare"}`, true, ""},
	} {
		facts := chain.Enrich(jsonDims(t, c.record))
		admit, filter, reason := chain.Admit(c.plan, &facts)
		if admit != c.admit || (!admit && reason != c.reason) {
			t.Errorf("%s: admit=%v by %s/%s, want admit=%v %s; keys %v", c.name, admit, filter, reason, c.admit, c.reason, facts.HostKeys())
		}
	}
	moved := chain.Enrich(jsonDims(t, `{"bk_agent_id":"agent-moved"}`))
	if !moved.HostUnresolved {
		t.Error("an agent the cache could not place is not marked unresolved: its rejection would be a verdict")
	}
}

// A record whose bk_host_id is there but empty is placed by its agent, and
// Python's fuller returns there (fullers.py:61-74): the service instance the
// record also names is not asked, so its host - here a spare one, in another
// module - neither replaces the agent's host's topology nor is the host whose
// state is judged.
func TestAHostPlacedByItsAgentIsNotReplacedByTheRecordsServiceInstance(t *testing.T) {
	onSpare := `{"service_instance_id":7301,"bk_host_id":720001,"ip":"192.0.2.171","bk_cloud_id":0,` +
		`"topo_link":{"module|85":[{"bk_obj_id":"module","bk_inst_id":85}]}}`
	store := agentStoreWithInstances(map[string]string{"agent-live": "720002"}, []string{"7301", onSpare})
	chain := instanceChain(t, store, "备用机")
	record := `{"bk_host_id":"","bk_agent_id":"agent-live","bk_target_service_instance_id":"7301"}`
	module91 := scopeOf(admission.TargetScopeTopoNode, admission.TargetScopeInclude, "module|91")
	for _, plan := range []admission.PlanContext{{}, module91} {
		facts := chain.Enrich(jsonDims(t, record))
		if admit, filter, reason := chain.Admit(plan, &facts); !admit {
			t.Errorf("rejected by %s/%s, want the agent's host's record admitted; topology %v", filter, reason, facts.Attributes)
		}
	}
}

// The host's own agent is read as any scalar: a record whose agent is written
// as a number is filed, and places a record by that agent, rather than being
// refused for the shape of one field.
func TestAHostWhoseAgentIsANumberIsFiledAndPlaced(t *testing.T) {
	record := `{"bk_host_id":720009,"bk_host_innerip":"192.0.2.179","bk_cloud_id":0,"bk_biz_id":999,"bk_agent_id":12345,` +
		`"bk_state":"运营中[需告警]","topo_link":{"module|91":[{"bk_obj_id":"module","bk_inst_id":91}]}}`
	store := storeWith([]string{"720009", record}, nil)
	store.index.byAgent = map[string]string{"12345": "720009"}
	if refused := store.index.Refused().Hosts; refused != 0 {
		t.Fatalf("a numeric agent refused the record: %d refused", refused)
	}
	if host, found, _ := store.index.LookupAgent("12345"); !found || host.HostID != "720009" {
		t.Fatalf("numeric agent placed %+v, %v", host, found)
	}
}

// An agent hash that could not be read leaves an agent-named record's facts
// unavailable, by name, rather than unknown: a topology target admits it under
// that name instead of failing it for want of a chain.
func TestAnUnreadableAgentHashLeavesAgentNamedRecordsUnavailable(t *testing.T) {
	store := agentStore(nil)
	store.index.agentsUnreadable = true
	chain := instanceChain(t, store, "备用机")
	facts := chain.Enrich(jsonDims(t, `{"bk_agent_id":"agent-spare"}`))
	module91 := scopeOf(admission.TargetScopeTopoNode, admission.TargetScopeInclude, "module|91")
	if admit, _, reason := chain.Admit(module91, &facts); !admit || reason != admission.FactsUnavailableHostIndex {
		t.Fatalf("admit=%v reason=%s, want admitted under %s", admit, reason, admission.FactsUnavailableHostIndex)
	}
}
