// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/runtimeconfig"
)

func TestDynamicFactoryRetainsRemovedSeverityForLifecycle(t *testing.T) {
	state := runtimeconfig.NewSeverity(config.DefaultSeverityConfig())
	if err := state.Install(runtimeconfig.Snapshot{Enabled: true, NativeNames: true, Severity: config.SeverityConfig{DefaultSeverity: "warning", Levels: []config.SeverityLevel{{Name: "warning", Priority: 1}}}}); err != nil {
		t.Fatal(err)
	}
	source := testSource()
	source.SeverityMapping = map[string]string{"P0": "removed"}
	factory, err := NewDynamicEventFactory(source, state)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"P0", "removed"} {
		event, err := factory.Build(RawEventMessage{RecordID: "record", BKTenantID: "tenant-1", ReceivedAt: time.Now(), Payload: []byte(`{}`)}, EventDraft{BKTenantID: "tenant-1", Title: "test", SourceAlertID: "source-alert", Evaluations: []domain.EventEvaluation{{Severity: raw, Action: domain.EventActionTriggered}}, Dimensions: domain.DimensionMap{"host": domain.NewStringScalar("host-1")}})
		if err != nil {
			t.Fatal(err)
		}
		if event.Evaluations[0].Severity != "removed" {
			t.Fatalf("unknown severity was silently mapped: %+v", event.Evaluations)
		}
	}
}
