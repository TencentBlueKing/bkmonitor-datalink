// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A strategy whose target shape this build cannot derive a no-data roster for
// keeps detecting its thresholds through the production wiring, and its
// no-data half is off and named, not running emptily.
//
// This is the user's ruling of 2026-09-16 (no-data capability decomposition,
// section 5.9 item 1): no-data fails locally and never takes the anomaly
// detection with it; a suspended Plan is accepted, carries no no-data
// section, and is named on a no_data_suspended line by strategy. The shapes
// are the ones section 5.7 refuses because the backend has an expected set
// for them that this cut does not build (rows d and e), and one more the
// backend cannot read as addresses either (base.py:185).
//
// Each case compiles the stored strategy, runs one round on a real
// redis-server with host A over its threshold and host B silent, and reads
// what left the process: A's threshold alert, nothing about no-data, no
// no-data round counted, and the named line. The control row is a shape the
// first cut does derive, under the same fixture, which raises B's absence:
// without it, a fixture that judged no-data nowhere would pass every row.
func TestASuspendedNoDataShapeStillDetectsThresholds(t *testing.T) {
	topology := `{"module|31":[{"bk_obj_id":"module","bk_inst_id":31},{"bk_obj_id":"set","bk_inst_id":12},` +
		`{"bk_obj_id":"biz","bk_inst_id":2}]}`
	topologyTarget := func(item map[string]any) {
		item["target"] = []any{[]any{map[string]any{"field": "host_topo_node", "method": "eq",
			"value": []any{map[string]any{"bk_obj_id": "set", "bk_inst_id": 12}}}}}
	}
	for name, test := range map[string]struct {
		strategy  lifecycleStrategy
		topology  string
		suspended bool
	}{
		"a static host list under the address alone": {strategy: lifecycleStrategy{
			targets: []string{lifecycleHostA, lifecycleHostB}, dimensions: []string{"bk_target_ip"}}, suspended: true},
		"a topology target under the host pair": {strategy: lifecycleStrategy{
			dimensions: lifecycleHostPair, edit: topologyTarget}, topology: topology, suspended: true},
		"a topology target under the host pair and one dimension more": {strategy: lifecycleStrategy{
			dimensions: []string{"bk_target_ip", "bk_target_cloud_id", "appid"}, edit: topologyTarget},
			topology: topology, suspended: true},
		"a static list naming hosts by identifier only": {strategy: lifecycleStrategy{
			dimensions: lifecycleHostPair, edit: func(item map[string]any) {
				item["target"] = []any{[]any{map[string]any{"field": "bk_target_ip", "method": "eq",
					"value": []any{map[string]any{"bk_host_id": 1}, map[string]any{"bk_host_id": 2}}}}}
			}}, suspended: true},
		"control: a static host list under the host pair": {strategy: lifecycleStrategy{
			targets: []string{lifecycleHostA, lifecycleHostB}, dimensions: lifecycleHostPair}},
	} {
		t.Run(name, func(t *testing.T) {
			strategy := test.strategy
			strategy.revision, strategy.continuous = 7, 1
			fixture := startLifecycleFixture(t, strategy, lifecycleHostRecords(test.topology))
			fixture.serve(false, 95, lifecycleHostA)
			fixture.mustAttempt(1)

			if raised := fixture.thresholdEventsFor(lifecycleHostA); len(raised) != 1 || raised[0].EventKind != contract.TriggerEventAbnormal {
				t.Fatalf("host A at 95 against a threshold of 80 sent %+v, want one ABNORMAL: the threshold detection "+
					"is what a no-data suspension must leave running", raised)
			}
			outcomes := fixture.noDataOutcomes()
			named := fixture.suspendedLines()
			if !test.suspended {
				if sent := fixture.noDataEventsFor(lifecycleHostB); len(sent) != 1 || outcomes["EVALUATED"] != 1 || len(named) != 0 {
					t.Fatalf("control: B's no-data events %+v, outcomes %v, suspended lines %v; want B raised, one "+
						"EVALUATED round and nothing named", sent, outcomes, named)
				}
				return
			}
			if sent := fixture.allNoDataEvents(); len(sent) != 0 {
				t.Fatalf("no-data events %+v from a strategy whose no-data half is suspended", sent)
			}
			if len(outcomes) != 0 {
				t.Fatalf("no-data rounds %v from a Plan that carries no no-data section", outcomes)
			}
			if len(named) != 1 || named[0] != "1001" {
				t.Fatalf("no_data_suspended lines name %v, want the strategy named once", named)
			}
		})
	}
}

// suspendedLines is the strategy every no_data_suspended line the bundles
// wrote names.
func (fixture *lifecycleFixture) suspendedLines() []string {
	fixture.observationsMu.Lock()
	defer fixture.observationsMu.Unlock()
	var lines []string
	for _, observation := range fixture.observations {
		if observation.Stage == observability.StageNoDataSuspended {
			lines = append(lines, observation.Trace.StrategyID)
		}
	}
	return lines
}
