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
	"context"
	"testing"
)

func TestStandardCleanerMapsKnownFields(t *testing.T) {
	t.Parallel()
	cleaner := StandardCleaner{}
	message := RawEventMessage{Payload: validPayload()}
	draft, err := cleaner.Clean(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Evaluations[0].Action != "triggered" || draft.Title != "CPU high" || draft.BKTenantID != "tenant-1" ||
		draft.SourceEventID != "source-event-1" || draft.SourceAlertID != "source-alert-1" ||
		draft.SubjectSystem != "cmdb" || draft.SubjectType != "host" || draft.SubjectID != "1" {
		t.Fatalf("draft=%#v", draft)
	}
}

func TestStandardCleanerPreservesNoDataDimension(t *testing.T) {
	t.Parallel()
	cleaner := StandardCleaner{}
	message := RawEventMessage{Payload: []byte(`{"evaluations":[{"action":"triggered","severity":"warning"}],"dimensions":{"__NO_DATA_DIMENSION__":true,"model_id":"cw-Service","model_inst_id":"2"}}`)}
	draft, err := cleaner.Clean(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	marker, present := draft.Dimensions["__NO_DATA_DIMENSION__"]
	if enabled, valid := marker.BoolValue(); !present || !valid || !enabled {
		t.Fatalf("no-data marker was lost: %#v", draft.Dimensions)
	}
}

func TestStandardCleanerAdditionalDimensions(t *testing.T) {
	t.Parallel()
	cleaner := StandardCleaner{}
	valid := RawEventMessage{Payload: []byte(`{"dimensions":{"host":"host-1"},"evaluations":[{"action":"triggered"}],"extra_data":{"additional_dimensions":{"bk_host_id":101}}}`)}
	draft, err := cleaner.Clean(context.Background(), valid)
	if err != nil {
		t.Fatalf("valid additional dimensions: %v", err)
	}
	if string(draft.ExtraData["additional_dimensions"]) != `{"bk_host_id":101}` {
		t.Fatalf("additional dimensions=%s", draft.ExtraData["additional_dimensions"])
	}
	for name, payload := range map[string]string{
		"nested value":  `{"dimensions":{},"evaluations":[{"action":"triggered"}],"extra_data":{"additional_dimensions":{"host":{"id":1}}}}`,
		"duplicate key": `{"dimensions":{"host":"host-1"},"evaluations":[{"action":"triggered"}],"extra_data":{"additional_dimensions":{"host":"host-2"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := cleaner.Clean(context.Background(), RawEventMessage{Payload: []byte(payload)}); err == nil {
				t.Fatal("invalid additional dimensions accepted")
			}
		})
	}
}

func TestStandardCleanerJSONValidationAndUnknownFields(t *testing.T) {
	t.Parallel()
	cleaner := StandardCleaner{}
	message := RawEventMessage{}
	for _, body := range [][]byte{[]byte(`[]`), []byte(`{"title":"x","title":"y"}`), []byte(`{"title":"x"} {}`)} {
		message.Payload = body
		if _, err := cleaner.Clean(context.Background(), message); err == nil {
			t.Fatalf("invalid payload accepted: %s", body)
		}
	}
	message.Payload = []byte(`{"title":"x","severity":"warning"}`)
	if _, err := cleaner.Clean(context.Background(), message); err == nil {
		t.Fatal("standard payload without action was accepted")
	}
	message.Payload = []byte(`{"bk_tenant_id":"payload-tenant","evaluations":[{"action":"triggered"}],"fingerprint":"payload-value","title":"x","unknown":{"nested":true}}`)
	draft, err := cleaner.Clean(context.Background(), message)
	if err != nil {
		t.Fatalf("standard payload with unknown fields: %v", err)
	}
	if draft.Title != "x" || draft.SourceEventID != "" || draft.SourceAlertID != "" {
		t.Fatalf("unknown fields affected draft: %#v", draft)
	}
}

func validPayload() []byte {
	return []byte(`{"alert_id":"source-alert-1","bk_tenant_id":"tenant-1","content":"usage high","dimensions":{"host":"host-1"},"evaluations":[{"action":"triggered","severity":"P2"}],"event_id":"source-event-1","extra_data":{},"labels":{"team":"ops"},"occurred_at":"2026-09-01T00:00:00Z","produced_at":"2026-09-01T00:00:01Z","subject":{"id":"1","name":"host-1","system":"cmdb","type":"host"},"title":"CPU high"}`)
}
