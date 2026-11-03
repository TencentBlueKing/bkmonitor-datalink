// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The no_data_suspended line says why the strategy's no-data half is off, as
// it reaches every observer - the log, the page and the tracker read the same
// normalized observation.
//
// The user's ruling (no-data capability decomposition, section 5.9 item 1)
// asks for one line per change naming the strategy and the reason
// (NO_DATA_CONFIG_INVALID or NO_DATA_ROSTER_UNSUPPORTED); the two are
// different work for different people - a configuration to fix against a
// capability this build does not have - and a line without the reason says
// only that something is off.
func TestTheNoDataSuspendedLineCarriesItsReason(t *testing.T) {
	for reason, strategy := range map[string]lifecycleStrategy{
		"NO_DATA_ROSTER_UNSUPPORTED": {targets: []string{lifecycleHostA, lifecycleHostB}, dimensions: []string{"bk_target_ip"}},
		"NO_DATA_CONFIG_INVALID": {targets: []string{lifecycleHostA, lifecycleHostB}, dimensions: lifecycleHostPair,
			edit: func(item map[string]any) {
				item["no_data_config"].(map[string]any)["continuous"] = "many"
			}},
	} {
		t.Run(reason, func(t *testing.T) {
			strategy.revision, strategy.continuous = 7, 1
			fixture := startLifecycleFixture(t, strategy, nil)
			fixture.observationsMu.Lock()
			defer fixture.observationsMu.Unlock()
			var lines []observability.Observation
			for _, observation := range fixture.observations {
				if observation.Stage == observability.StageNoDataSuspended {
					lines = append(lines, observation)
				}
			}
			if len(lines) != 1 || lines[0].Trace.StrategyID != "1001" {
				t.Fatalf("no_data_suspended lines = %+v, want one naming the strategy", lines)
			}
			if facts := lines[0].SourceWithheld; facts == nil || facts.Reason != reason {
				t.Fatalf("the no_data_suspended line carries %+v, want reason %s", facts, reason)
			}
		})
	}
}
