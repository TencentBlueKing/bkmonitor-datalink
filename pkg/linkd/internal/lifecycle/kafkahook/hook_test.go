// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafkahook

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
)

func TestHookEmitsV1AlertChange(t *testing.T) {
	producer := &fakeProducer{}
	hook := newHook(Config{Brokers: []string{"localhost:9092"}, Topic: "alerts", MaxMessageBytes: 1 << 20}, producer)
	alert := testAlert()
	input := lifecycle.FinalHookInput{Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event-1"}, Alert: alert, Outcome: lifecycle.OutcomeAlertCreated}
	result, err := hook.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID == "" || len(producer.records) != 1 {
		t.Fatalf("result=%#v records=%d", result, len(producer.records))
	}
	record := producer.records[0]
	if len(record.Key) == 0 || string(record.Key) == result.MessageID {
		t.Fatalf("partition key=%q", record.Key)
	}
	if !record.Timestamp.Equal(alert.UpdateAt) {
		t.Fatalf("timestamp=%s", record.Timestamp)
	}
	var message Message
	if err := json.Unmarshal(record.Value, &message); err != nil {
		t.Fatal(err)
	}
	if message.SchemaVersion != "1" || message.UpdateAt != alert.UpdateAt || message.Cause.ID != "event-1" || message.Alert.AlertID != alert.AlertID {
		t.Fatalf("message=%#v", message)
	}
	again, err := hook.Execute(context.Background(), input)
	if err != nil || again.MessageID != result.MessageID {
		t.Fatalf("stable message id=%#v,%v", again, err)
	}
}

func testAlert() domain.Alert {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return domain.Alert{AlertID: "alert-1", BKTenantID: "tenant-1", EventSourceID: "source", Fingerprint: "fp", Title: "CPU high", Severity: "warning", Dimensions: domain.DimensionMap{}, Labels: domain.DimensionMap{}, ExtraData: domain.JSONObject{}, Status: domain.AlertStatusActive, LatestEventID: "event-1", LastOccurredAt: now, UpdateAt: now, TriggerEventID: "event-1", BeginAt: now, CreateAt: now, EnrichStatus: domain.EnrichStatusSucceeded, Enrich: domain.JSONObject{}}
}

type fakeProducer struct{ records []*kgo.Record }

func (p *fakeProducer) ProduceSync(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
	p.records = append(p.records, records...)
	results := make(kgo.ProduceResults, len(records))
	for i, record := range records {
		results[i].Record = record
	}
	return results
}

func (*fakeProducer) Close() {}
