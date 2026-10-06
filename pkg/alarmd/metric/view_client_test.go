// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"math"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// The Worker's view gauges: installs and failures by closed word with other,
// and objects missing as a number when the last install probed the catalog
// and NaN when it could not -- unknown is not 0.
func TestTheWorkerPublishesObjectsMissingAsUnknownWhenItCouldNotProbe(t *testing.T) {
	gather := func(counts ViewClientCounts) map[string]map[string]float64 {
		recorder := NewRecorder(BuildInfo{})
		recorder.SetViewClientSource(func() ViewClientCounts { return counts })
		families, err := recorder.registry.Gather()
		if err != nil {
			t.Fatal(err)
		}
		gathered := map[string]map[string]float64{}
		for _, family := range families {
			for _, series := range family.Metric {
				key := ""
				for _, pair := range series.Label {
					key += pair.GetValue()
				}
				if gathered[family.GetName()] == nil {
					gathered[family.GetName()] = map[string]float64{}
				}
				value := series.GetGauge().GetValue()
				if series.Counter != nil {
					value = series.GetCounter().GetValue()
				}
				gathered[family.GetName()][key] = value
			}
		}
		return gathered
	}
	probed := gather(ViewClientCounts{Connected: true, InstalledRevision: 9, ObjectsMissing: 3, ObjectsProbed: true,
		Installs: map[string]uint64{"snapshot": 1, "delta": 4, "weird": 2}, InstallFailures: map[string]uint64{"DELTA_BASE_MISMATCH": 1},
		Refusals: map[string]uint64{"NOT_LEADER": 2}})
	if probed["bkmonitor_alarmd_view_objects_missing"][""] != 3 || probed["bkmonitor_alarmd_view_installed_revision"][""] != 9 || probed["bkmonitor_alarmd_view_client_connected"][""] != 1 {
		t.Fatalf("probed gauges = %v %v %v", probed["bkmonitor_alarmd_view_objects_missing"], probed["bkmonitor_alarmd_view_installed_revision"], probed["bkmonitor_alarmd_view_client_connected"])
	}
	if installs := probed["bkmonitor_alarmd_view_install_total"]; installs["snapshot"] != 1 || installs["delta"] != 4 || installs["empty_delta"] != 0 || installs["other"] != 2 || len(installs) != 4 {
		t.Fatalf("installs = %v", installs)
	}
	if failures := probed["bkmonitor_alarmd_view_install_failure_total"]; failures["DELTA_BASE_MISMATCH"] != 1 || failures["SNAPSHOT_INVALID"] != 0 || len(failures) != len(viewClientInstallFailures)+1 {
		t.Fatalf("failures = %v", failures)
	}
	if refusals := probed["bkmonitor_alarmd_view_client_refusal_total"]; refusals["NOT_LEADER"] != 2 || len(refusals) != len(viewClientRefusals)+1 {
		t.Fatalf("refusals = %v", refusals)
	}
	unprobed := gather(ViewClientCounts{Connected: true, ObjectsMissing: 0, ObjectsProbed: false})
	if value := unprobed["bkmonitor_alarmd_view_objects_missing"][""]; !math.IsNaN(value) {
		t.Fatalf("objects missing without a probe = %v, want NaN", value)
	}
}

// The discovery miss words are spelled once, in the client; the metric's
// closed set is that list, so a word added to one cannot land on other in
// the other.
func TestTheDiscoveryMissWordsAreTheClientsList(t *testing.T) {
	if !reflect.DeepEqual(viewClientDiscoveryMisses, viewstream.DiscoveryMissReasons) {
		t.Fatalf("metric %v, client %v", viewClientDiscoveryMisses, viewstream.DiscoveryMissReasons)
	}
}
