// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"context"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
)

func TestEventSourceUsesEventSourceIDForLookupAndSourceEventIDForMeta(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	reader := &recordingAlarmSourceReader{name: "自定义告警源"}
	scope, err := enrich.NewScope(domain.Alert{
		AlertID: "alert-1", BKTenantID: "tenant-a", EventSourceID: "alarm-source-1", Fingerprint: "fp",
		Title: "alarm", Severity: "warning", Status: domain.AlertStatusActive,
		Dimensions: domain.DimensionMap{}, Labels: domain.DimensionMap{}, ExtraData: domain.JSONObject{},
		SourceEventID: "source-event-1", LatestEventID: "event-1", TriggerEventID: "event-1",
		LastOccurredAt: now, UpdateAt: now, BeginAt: now, CreateAt: now,
		EnrichStatus: domain.EnrichStatusPending, Enrich: domain.JSONObject{},
	}, enrich.Sources{AlarmSource: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (EventSource{}).Process(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if reader.tenantID != "tenant-a" || reader.sourceID != "alarm-source-1" {
		t.Fatalf("lookup tenant=%q source=%q", reader.tenantID, reader.sourceID)
	}
	if string(result.Value["source_id"]) != `"alarm-source-1"` ||
		string(result.Value["source_name"]) != `"自定义告警源"` ||
		string(result.Value["meta_info"]) != `"source-event-1"` {
		t.Fatalf("result=%#v", result)
	}
}

type recordingAlarmSourceReader struct {
	name               string
	tenantID, sourceID string
}

func (r *recordingAlarmSourceReader) GetAlarmSourceName(
	_ context.Context,
	tenantID, sourceID string,
) (string, bool, error) {
	r.tenantID, r.sourceID = tenantID, sourceID
	return r.name, true, nil
}
