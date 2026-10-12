// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

// A target rejection is a verdict the target-scope close may act on only when
// the facts it was decided on were there (decision-024 section 1, "the cache
// must be trusted": a host the cache has not found may be one it has not
// learned yet, so its alert is not closed). This holds whichever way the
// record names its host and on both target paths. Python's own periodic close
// would close here (close.py:372-382); decision-024 departs from it on purpose.

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type definitiveMembers map[string]struct{}

func (m definitiveMembers) Contains(key string) bool { _, ok := m[key]; return ok }
func (definitiveMembers) Definitive() bool           { return true }

func definitiveChain(store *Store) *admission.Chain {
	return admission.NewChain(
		[]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(store), NewServiceInstanceTopologyFuller(store)},
		[]admission.Filter{admission.TargetScopeFilter{}, admission.TargetPlanFilter{}})
}

func rejectionOf(t *testing.T, chain *admission.Chain, plan admission.PlanContext, record map[string]json.RawMessage) (bool, string) {
	t.Helper()
	facts := chain.Enrich(record)
	admit, filter, reason := chain.Admit(plan, &facts)
	if admit {
		t.Fatalf("setup: %v admitted", record)
	}
	return admission.DefinitelyOutside(plan, &facts, filter, reason), reason
}

func TestARejectionOfAHostTheCacheDoesNotKnowIsNoVerdict(t *testing.T) {
	store := storeWith(
		[]string{"192.0.2.148|0", monitoredByIDHost, "700002", monitoredByIDHost},
		[]string{"5001", `{"service_instance_id":5001,"bk_host_id":700002,"ip":"192.0.2.148","bk_cloud_id":0,"topo_link":{"module|91":[{"bk_obj_id":"module","bk_inst_id":91}]}}`},
	)
	chain := definitiveChain(store)
	topoScope := scopeOf(admission.TargetScopeTopoNode, admission.TargetScopeInclude, "module|85")
	hostPlan := admission.PlanContext{TargetPlan: &admission.TargetPlanContext{
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true},
		Members:  definitiveMembers{"101": {}}}}
	for _, c := range []struct {
		name   string
		plan   admission.PlanContext
		record map[string]json.RawMessage
		want   bool
	}{
		// Not found: no verdict, on either path.
		{"legacy topology, unknown host by ip alias", topoScope, dims("ip", `"192.0.2.199"`, "bk_cloud_id", `0`), false},
		{"legacy topology, unknown host by address without cloud", topoScope, dims("bk_target_ip", `"192.0.2.199"`), false},
		{"legacy topology, unknown host id", topoScope, dims("bk_host_id", `"800001"`), false},
		{"legacy topology, unknown service instance", topoScope, dims("bk_target_service_instance_id", `"9999"`), false},
		{"target plan host_id, unknown host id", hostPlan, dims("bk_host_id", `"999"`), false},
		{"target plan host_id, unknown host by address", hostPlan, dims("bk_target_ip", `"192.0.2.199"`, "bk_target_cloud_id", `"0"`), false},
		// Found, and outside: the target's own verdict.
		{"legacy topology, known host by alias", topoScope, dims("ip", `"192.0.2.148"`, "bk_cloud_id", `0`), true},
		{"legacy topology, known host id", topoScope, dims("bk_host_id", `"700002"`), true},
		{"legacy topology, known service instance", topoScope, dims("bk_target_service_instance_id", `"5001"`), true},
		{"target plan host_id, known host id", hostPlan, dims("bk_host_id", `"700002"`), true},
		{"target plan host_id, known host by address", hostPlan, dims("bk_target_ip", `"192.0.2.148"`, "bk_target_cloud_id", `"0"`), true},
	} {
		if got, reason := rejectionOf(t, chain, c.plan, c.record); got != c.want {
			t.Errorf("%s: definitive = %v (reason %s), want %v", c.name, got, reason, c.want)
		}
	}
}
