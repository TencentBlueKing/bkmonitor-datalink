// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// A Plan outside its effective time whose set the copy has read but never
// calibrated - the link's reconcile failing - has alerts open that cannot be
// listed. That is named and counted under evidence_unavailable, once per
// Plan per step, kept for maintenance.get with the strategy, and nothing is
// sent: no close is addressed from an unlisted set. A calibrated set with no
// alerts is "nothing to close" and is not counted; a calibrated set with an
// alert closes it and is not counted either.
func TestAnInactivePlanWhoseAlertsCannotBeListedIsCountedAndNotClosed(t *testing.T) {
	alert := maintenanceAlert("native", "critical")
	for _, c := range []struct {
		name        string
		source      maintenanceTestIndex
		unavailable uint64
		sent        int
	}{
		{name: "the set is never calibrated", source: maintenanceTestIndex{alerts: []openalerts.Alert{alert}, reconcileErr: errors.New("reconcile: link answered 503")}, unavailable: 2},
		{name: "the set is calibrated and empty", source: maintenanceTestIndex{}},
		{name: "the set is calibrated with an alert", source: maintenanceTestIndex{alerts: []openalerts.Alert{alert}}, sent: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newMaintenanceTestFixtureFrom(t, maintenanceReadySnapshot, maintenanceTime(8, 0), c.source)
			f.m.step(context.Background())
			f.m.step(context.Background())
			sent := 0
			for _, batch := range f.writer.batches {
				sent += len(batch)
			}
			if sent != c.sent {
				t.Fatalf("sent %+v, want %d closes", f.writer.batches, c.sent)
			}
			if got := f.m.counts[closeOutcomeEvidenceUnavailable]; got != c.unavailable {
				t.Fatalf("evidence_unavailable counted %d over two steps, want %d: counts %v", got, c.unavailable, f.m.counts)
			}
			kept := f.m.Reading().Recent[closeOutcomeEvidenceUnavailable]
			if c.unavailable == 0 {
				if len(kept) != 0 {
					t.Fatalf("kept %+v under evidence_unavailable, want nothing", kept)
				}
				return
			}
			if len(kept) != 1 || kept[0].StrategyID != "123" || kept[0].Count != 2 || kept[0].Error == "" {
				t.Fatalf("maintenance.get keeps %+v, want the strategy once, seen twice, with its reason", kept)
			}
		})
	}
}
