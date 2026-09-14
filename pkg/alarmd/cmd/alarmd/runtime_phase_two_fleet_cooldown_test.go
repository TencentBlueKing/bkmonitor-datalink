// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The tracker puts an object holding a query cooldown in the demoted list and
// nowhere else, so a gauge that walked the anomaly list alone read zero from
// every tracker of this build while the object page listed the pool. The
// same object counts as one cooldown object whichever list it is in, and a
// demoted object that carries no cooldown evidence is not counted as one.
func TestFleetVerdictCountsCooldownObjectsInBothColumns(t *testing.T) {
	at := time.Date(2026, 9, 14, 2, 3, 0, 0, time.UTC)
	held := fleet.Anomaly{
		QueryGroup: "held", Kind: fleet.KindQueryCooldown, Since: at.Add(-time.Minute),
		QueryCooldown: &observability.QueryCooldownFacts{Until: at.Add(time.Minute)},
	}
	for _, column := range []struct {
		name string
		view fleet.View
	}{
		{name: "in the anomaly list", view: fleet.View{Covered: 1, Determined: 1, Anomalies: []fleet.Anomaly{held}}},
		{name: "in the demoted list", view: fleet.View{Covered: 1, Determined: 1, Demoted: []fleet.Anomaly{held}, DemotedTotal: 1}},
	} {
		t.Run(column.name, func(t *testing.T) {
			verdict := fleetVerdictOf(column.view, at)
			if verdict.QueryCooldown == nil {
				t.Fatal("cooldown count absent for a covered view")
			}
			if *verdict.QueryCooldown != 1 {
				t.Fatalf("cooldown objects = %d, want the one object %s", *verdict.QueryCooldown, column.name)
			}
		})
	}

	withoutEvidence := fleet.View{Covered: 1, Determined: 1, Demoted: []fleet.Anomaly{{QueryGroup: "bare", Kind: fleet.KindQueryCooldown, Since: at}}}
	if verdict := fleetVerdictOf(withoutEvidence, at); verdict.QueryCooldown == nil || *verdict.QueryCooldown != 0 {
		t.Fatalf("a demoted object carrying no cooldown evidence was counted as a cooldown object: %+v", verdict.QueryCooldown)
	}
}
