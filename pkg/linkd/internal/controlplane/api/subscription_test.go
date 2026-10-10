// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

func TestSubscriptionChangeRequiresExplicitBooleanPerRequest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		flag    any
		present bool
		status  int
	}{
		{"omitted", nil, false, http.StatusBadRequest},
		{"false", false, true, http.StatusBadRequest},
		{"true", true, true, http.StatusAccepted},
		{"wrong type", "true", true, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := &hookDocuments{map[string]json.RawMessage{}, map[string]int{}}
			svc := eventsource.New(docs, config.DefaultSeverityConfig())
			spec := config.EventSource{EventSourceID: "subscription-source", Enabled: true,
				Storage: config.EventSourceStorageConfig{Type: "kafka", Kafka: config.KafkaStorageConfig{Brokers: []string{"kafka:9092"}, Topic: "old-topic", ConsumerGroup: "existing-group"}}}.WithDefaults()
			first, err := svc.Apply(t.Context(), spec, 0, false, "create")
			if err != nil {
				t.Fatal(err)
			}
			old, err := svc.GetRelease(t.Context(), first.ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			spec.Storage.Kafka.Topic = "alarmd_event"
			body := map[string]any{"expected_revision": first.Revision, "spec": spec}
			if tc.present {
				body["allow_subscription_change"] = tc.flag
			}
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			api := (&API{Sources: svc, Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "test-admin"}}}).Handler()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/v1/event-sources/"+first.ID, bytes.NewReader(encoded))
			req.Header.Set("Internal-Token", testJWT(t, "test-admin"))
			out := httptest.NewRecorder()
			api.ServeHTTP(out, req)
			if out.Code != tc.status {
				t.Fatalf("status=%d want=%d", out.Code, tc.status)
			}
			current, err := svc.Get(t.Context(), first.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantVersion, wantTopic := int64(1), "old-topic"
			if tc.status == http.StatusAccepted {
				wantVersion, wantTopic = 2, "alarmd_event"
			}
			if current.Published != wantVersion || current.Spec.Storage.Kafka.Topic != wantTopic {
				t.Fatalf("wrong publication: %+v", current)
			}
			before, err := svc.GetRelease(t.Context(), first.ID, 1)
			if err != nil || !reflect.DeepEqual(before, old) {
				t.Fatalf("historical release mutated: %v", err)
			}
		})
	}
}
