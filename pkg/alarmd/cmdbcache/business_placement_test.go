// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

// A global strategy's host-target event is filed under the business of the
// host admission placed its record on (the global strategy design, sections
// 4.3 and 8 item 3). The host is placed by admission's own fullers, in
// Python's order - a true id, the agent, the service instance, the address
// (fullers.py:55-110) - so every way a record can name its host attributes
// it to the host admission judged.

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestAGlobalEventIsAttributedToTheHostAdmissionPlaced(t *testing.T) {
	host := `{"bk_host_id":730001,"bk_host_innerip":"192.0.2.181","bk_cloud_id":0,"bk_biz_id":11,"bk_agent_id":"agent-730001",
"bk_state":"运营中[需告警]","topo_link":{"module|91":[{"bk_obj_id":"module","bk_inst_id":91}]}}`
	instance := `{"service_instance_id":7302,"bk_host_id":730001,"ip":"192.0.2.181","bk_cloud_id":0,` +
		`"topo_link":{"module|91":[{"bk_obj_id":"module","bk_inst_id":91}]}}`
	store := storeWith([]string{"730001", host, "192.0.2.181|0", host}, []string{"7302", instance})
	store.index.byAgent = map[string]string{"agent-730001": "730001"}
	lookups := admission.BusinessLookups{Hosts: NewHostBusinessLookup(store)}
	target := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleHostID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{"730001"}}
	const planBusiness = "524"
	for _, c := range []struct {
		name   string
		record string
		want   string
	}{
		{"a true id", `{"bk_host_id":"730001"}`, "11"},
		{"an address with its cloud", `{"bk_target_ip":"192.0.2.181","bk_target_cloud_id":"0"}`, "11"},
		{"an address without its cloud", `{"bk_target_ip":"192.0.2.181"}`, "11"},
		{"the alias spelling", `{"ip":"192.0.2.181","bk_cloud_id":0}`, "11"},
		{"the agent alone", `{"bk_agent_id":"agent-730001"}`, "11"},
		{"the service instance alone", `{"bk_target_service_instance_id":"7302"}`, "11"},
		// A true id the cache does not know is that host and no other:
		// admission drops it, and the address beside it is not consulted.
		{"an unknown id beside a known address", `{"bk_host_id":"999","bk_target_ip":"192.0.2.181","bk_target_cloud_id":"0"}`, planBusiness},
	} {
		got := admission.AttributeBusiness(target, nil, planBusiness, jsonDims(t, c.record), lookups)
		if got.BusinessID != c.want {
			t.Errorf("%s: attributed %+v, want business %s", c.name, got, c.want)
		}
	}

	// Past the staleness bound admission does not decide on the index, but
	// attribution is a label, and the held index still places the host.
	store.now = func() time.Time { return store.index.BuiltAt().Add(store.maxAge + time.Second) }
	if got := admission.AttributeBusiness(target, nil, planBusiness, jsonDims(t, `{"bk_host_id":"730001"}`), lookups); got.BusinessID != "11" {
		t.Errorf("past the staleness bound: attributed %+v, want the host's business from the held index", got)
	}
	// An index that holds no host places nothing: the event falls through.
	store.index = newIndexBuilder(store.index.BuiltAt()).index
	if got := admission.AttributeBusiness(target, nil, planBusiness, jsonDims(t, `{"bk_host_id":"730001"}`), lookups); got.BusinessID != planBusiness {
		t.Errorf("an empty index: attributed %+v, want the strategy's own business", got)
	}
}
