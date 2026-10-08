// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// The strategy's own tenant is what an ip_cloud plan is read under: a plan
// naming the strategy's tenant, whatever it is, is read, and one naming
// another is refused at the plan's tenant.
func TestAnIPCloudPlanIsReadUnderTheSourcesOwnTenant(t *testing.T) {
	document := func(tenant string) json.RawMessage {
		return json.RawMessage(`{"items":[{"query_configs":[],"target_plan":{"schema_version":1,"bk_tenant_id":"` + tenant + `",
			"model_id":"cw-Host","target_rule":"ip_cloud","failure_policy":"no_match","static_targets":[{"bk_host_id":501}],
			"dynamic_groups":[],"dynamic_topologies":[]}}]}`)
	}
	source := SourceStrategy{SourceID: "7", Document: document("tenant-z"), Identity: SourceIdentity{TenantID: "tenant-z", BusinessID: "2"}}
	if compiled := compileTargetPlanDocument(source); compiled.refusal != nil || compiled.plan == nil || compiled.plan.TenantID != "tenant-z" {
		t.Fatalf("the source's own tenant: %+v", compiled)
	}
	source.Document = document("tenant-y")
	compiled := compileTargetPlanDocument(source)
	if compiled.refusal == nil || compiled.refusal.Reason != targetplan.ReasonTenantMismatch || compiled.refusal.FieldPath != "items[0].target_plan.bk_tenant_id" {
		t.Fatalf("another tenant: %+v", compiled.refusal)
	}
}
