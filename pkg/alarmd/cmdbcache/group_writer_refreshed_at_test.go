// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

// The group writer adds a top-level refreshed_at (epoch seconds) to every
// document. The reader decodes the fields it knows and ignores the rest, so a
// document carrying it reads exactly as one without it - through the decoder
// and through the store and resolver a Plan reads it by.

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

func TestAGroupDocumentCarryingRefreshedAtReadsAsWithoutIt(t *testing.T) {
	without := `{"bk_tenant_id":"system","model_id":"cw-Host","bk_obj_id":"cw-Host","model_inst_ids":["101","102"],` +
		`"member_list":[{"model_id":"cw-Host","model_inst_id":"101","bk_host_id":101},{"model_id":"cw-Host","model_inst_id":"102","bk_host_id":102}]}`
	with := `{"bk_tenant_id":"system","model_id":"cw-Host","bk_obj_id":"cw-Host","model_inst_ids":["101","102"],` +
		`"member_list":[{"model_id":"cw-Host","model_inst_id":"101","bk_host_id":101},{"model_id":"cw-Host","model_inst_id":"102","bk_host_id":102}],` +
		`"refreshed_at":1791545000}`
	at := time.Unix(1000, 0)
	plain, carrying := decodeGroup("g", []byte(without), at), decodeGroup("g", []byte(with), at)
	if carrying.Unavailable != "" || carrying.Dropped != 0 || !reflect.DeepEqual(carrying.Members, plain.Members) || len(carrying.Members) != 2 {
		t.Fatalf("document with refreshed_at = %+v, want the same snapshot as without it: %+v", carrying, plain)
	}

	clock := func() time.Time { return at }
	client := &groupClient{values: map[string]string{"cw:dynamic_group:g": with}}
	reader, _ := NewGroupReader(client, "cw:")
	groups, _ := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound, Now: clock})
	plan := hostPlan(contract.TargetPlanRuleHostID)
	plan.DynamicGroups = []string{"g"}
	resolution := NewTargetResolver(groups, nil, clock).Resolve(context.Background(), plan, time.Minute)
	if group := selector(resolution, targetplan.SelectorKindGroup, "g"); group.State != targetplan.SelectorOK || !resolution.Contains("101") || !resolution.Contains("102") {
		t.Fatalf("group with refreshed_at resolved %+v", group)
	}
}
