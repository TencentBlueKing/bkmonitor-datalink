// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const selectorGroupPrefix = "lifecycle_groups:"

// hostGroupDocument is a dynamic group document as the group writer stores it,
// holding the given hosts; none is the writer stating the group empty.
func hostGroupDocument(hosts ...string) string {
	ids, members := "", ""
	for index, host := range hosts {
		if index > 0 {
			ids += ","
			members += ","
		}
		ids += `"` + host + `"`
		members += `{"model_id":"cw-Host","model_inst_id":"` + host + `","bk_host_id":` + host + `}`
	}
	return `{"model_id":"cw-Host","model_inst_ids":[` + ids + `],"member_list":[` + members + `]}`
}

// hostIDGroupKey is the no-data group key of a host-identity target member,
// as the memory stores it.
func hostIDGroupKey(id string) string {
	return "bk_host_id=" + id + "," + contract.NoDataDimensionTag + "=true"
}

// A target plan member stays expected while anything in the plan still
// selects it, whichever selector went empty; only a member nothing selects
// any more has its open absence closed (decision-017 section 4, boundary 1:
// "a group that is empty does not mean the target left - it may be selected
// by the static targets, another group or a topology; only once every
// resolution succeeded and the merged, deduplicated result confirms the
// target is outside may the absence be ended, A8 once").
//
// The plan names host 1 statically and two dynamic groups: g1 holding hosts 1,
// 2 and 3, g2 holding host 2. No host reports, so all three are absent from
// round 1 with continuous 1. Then the group writer states g1 empty. Host 1 is
// still selected by the static list and host 2 by g2: both stay expected,
// their absences open since round 1, raised again on round 2. Host 3 was in g1
// alone: it leaves the expected set, and its absence is closed and forgotten.
//
// A new process runs round 2, so the empty g1 is what its group store reads
// first. The close is read where it is decided, in the memory: host 3 is
// gone from it and was not raised again, hosts 1 and 2 keep their round 1
// absences. It is also read where it lands: host 3's alert gets its one
// RECOVERY on round 2, under the identity round 1 raised it with, although
// the process that sends it never raised it and has read no open alert set.
func TestAMemberAnotherSelectorStillHoldsIsNotClosedWhenOneGroupEmpties(t *testing.T) {
	strategy := lifecycleStrategy{revision: 7, continuous: 1, dimensions: []string{"bk_host_id"},
		edit: func(item map[string]any) {
			item["query_configs"].([]any)[0].(map[string]any)["agg_dimension"] = []any{"bk_host_id"}
			item["target_plan"] = map[string]any{"schema_version": 1, "model_id": "cw-Host", "target_rule": "host_id",
				"failure_policy": "no_match", "dynamic_topologies": []any{},
				"static_targets": []any{map[string]any{"bk_host_id": 1}},
				"dynamic_groups": []any{map[string]any{"dynamic_group_id": "g1"}, map[string]any{"dynamic_group_id": "g2"}}}
		}}
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	for id, document := range map[string]string{"g1": hostGroupDocument("1", "2", "3"), "g2": hostGroupDocument("2")} {
		if err := client.Set(ctx, selectorGroupPrefix+"dynamic_group:"+id, document, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	fixture := startLifecycleFixtureOn(t, address, client, strategy, nil, func(cfg *config.Config) {
		prefix := selectorGroupPrefix
		cfg.PlatformCache.DynamicGroupKeyPrefix = &prefix
	})
	fixture.mu.Lock()
	fixture.byHostID = true
	fixture.mu.Unlock()
	fixture.serve(false, 5)
	opened := fixture.evaluationAt(1)

	fixture.mustAttempt(1)
	for _, host := range []string{"1", "2", "3"} {
		sent := fixture.noDataEventsWhere("bk_host_id", host)
		if len(sent) != 1 || sent[0].EventKind != contract.TriggerEventAbnormal {
			t.Fatalf("round 1 sent host %s's no-data events %+v, want one ABNORMAL: every member is expected", host, sent)
		}
		if firstAbsent, _ := fixture.absenceOfGroup(hostIDGroupKey(host)); firstAbsent != opened {
			t.Fatalf("after round 1 host %s's absence starts at %d, want %d", host, firstAbsent, opened)
		}
	}

	if err := client.Set(ctx, selectorGroupPrefix+"dynamic_group:g1", hostGroupDocument(), 0).Err(); err != nil {
		t.Fatal(err)
	}
	fixture.replace("alarmd-worker-0")
	fixture.mustAttempt(2)
	for _, host := range []string{"1", "2"} {
		sent := fixture.noDataEventsWhere("bk_host_id", host)
		if len(sent) != 2 || sent[1].EventKind != contract.TriggerEventAbnormal || sent[1].EvaluationTime != fixture.evaluationAt(2) {
			t.Fatalf("round 2 left host %s with no-data events %+v; want it raised again, still expected", host, sent)
		}
		if firstAbsent, held := fixture.absenceOfGroup(hostIDGroupKey(host)); !held || firstAbsent != opened {
			t.Fatalf("after g1 emptied, host %s's absence starts at %d (held %t); want it kept from %d: another selector "+
				"still holds it, so nothing closed it", host, firstAbsent, held, opened)
		}
	}
	third := fixture.noDataEventsWhere("bk_host_id", "3")
	if len(third) != 2 || third[1].EventKind != contract.TriggerEventRecovery ||
		third[1].EvaluationTime != fixture.evaluationAt(2) || third[1].DedupeMD5 != third[0].DedupeMD5 {
		t.Fatalf("host 3, which only g1 selected, has no-data events %+v; want its round 1 ABNORMAL and one "+
			"RECOVERY on round 2 under the same identity: its absence is closed once", third)
	}
	if firstAbsent, held := fixture.absenceOfGroup(hostIDGroupKey("3")); held {
		t.Fatalf("host 3 is still remembered with an absence from %d after nothing selects it; its absence is closed "+
			"once and forgotten", firstAbsent)
	}
}
