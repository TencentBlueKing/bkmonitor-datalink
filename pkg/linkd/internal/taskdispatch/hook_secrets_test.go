// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskdispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/kafkaclient"
)

type hookDocuments struct {
	data     map[string]json.RawMessage
	versions map[string]int
}

func (d *hookDocuments) Get(_ context.Context, kind, id string) (json.RawMessage, string, error) {
	k := kind + "/" + id
	b, ok := d.data[k]
	if !ok {
		return nil, "", eventsource.ErrNotFound
	}
	return b, fmt.Sprint(d.versions[k]), nil
}

func (d *hookDocuments) Put(_ context.Context, kind, id, expected string, b json.RawMessage) error {
	k := kind + "/" + id
	v := d.versions[k]
	if v == 0 && expected != "" || v > 0 && expected != fmt.Sprint(v) {
		return eventsource.ErrConflict
	}
	d.data[k] = append(json.RawMessage(nil), b...)
	d.versions[k]++
	return nil
}

func (d *hookDocuments) List(context.Context, string, string, int) ([]json.RawMessage, error) {
	return nil, nil
}

func TestHookSecretsSurviveConsoleEdit(t *testing.T) {
	docs := &hookDocuments{map[string]json.RawMessage{}, map[string]int{}}
	svc := eventsource.New(docs, config.SeverityConfig{})
	spec := config.EventSource{EventSourceID: "review-source", Enabled: true,
		Storage: config.EventSourceStorageConfig{Type: "kafka", Kafka: config.KafkaStorageConfig{Brokers: []string{"kafka:9092"}, Topic: "raw", ConsumerGroup: "review"}},
		Hooks: []config.HookConfig{
			{Name: "index", Type: config.HookTypeActiveAlertByStrategy, Config: config.HookParameters{Redis: &config.RedisConfig{Address: "redis:6379", Password: "synthetic-redis-secret"}, KeyPrefix: "review"}},
			{Name: "output", Type: config.HookTypeKafka, Config: config.HookParameters{Brokers: []string{"kafka:9092"}, Topic: "alerts", Security: kafkaclient.SecurityConfig{Protocol: "sasl_plaintext", SASL: &kafkaclient.SASLConfig{Mechanism: "plain", Username: "test", Password: "synthetic-kafka-secret"}}}},
		}}.WithDefaults()
	kac := spec.Hooks[1]
	kac.Name = "kac-output"
	kac.Type = config.HookTypeKAC
	spec.Hooks = append(spec.Hooks, kac)
	record, err := svc.Apply(t.Context(), spec, 0, false, "review")
	if err != nil {
		t.Fatal(err)
	}
	api := (&API{Sources: svc, Config: config.DispatchConfig{APIToken: "synthetic-admin"}}).Handler()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/event-sources/review-source", nil)
	req.Header.Set("Authorization", "Bearer synthetic-admin")
	rr := httptest.NewRecorder()
	api.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var edited eventsource.Record
	if err := json.Unmarshal(rr.Body.Bytes(), &edited); err != nil {
		t.Fatal(err)
	}
	// 模拟 Console 编辑：移除输入 Kafka security，仅修改副本数。
	edited.Spec.Storage.Kafka.Security = kafkaclient.SecurityConfig{}
	replicas := 1
	edited.Spec.Scheduling.Lifecycle.Replicas.Number = &replicas
	body, err := json.Marshal(Mutation{Expected: record.Revision, Spec: edited.Spec})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/v1/event-sources/review-source", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer synthetic-admin")
	rr = httptest.NewRecorder()
	api.ServeHTTP(rr, req)
	if rr.Code != 202 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	saved, err := svc.Get(t.Context(), spec.EventSourceID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Spec.Hooks[0].Config.Redis.Password != spec.Hooks[0].Config.Redis.Password {
		t.Errorf("Redis hook password overwritten with %q", saved.Spec.Hooks[0].Config.Redis.Password)
	}
	if saved.Spec.Hooks[2].Config.Security.SASL.Password != spec.Hooks[2].Config.Security.SASL.Password {
		t.Fatal("KAC hook password overwritten")
	}
	if saved.Spec.Hooks[1].Config.Security.SASL.Password != spec.Hooks[1].Config.Security.SASL.Password {
		t.Errorf("Kafka hook password overwritten with %q", saved.Spec.Hooks[1].Config.Security.SASL.Password)
	}
}

func TestAPIRejectsUnmatchedMaskedHookCredentials(t *testing.T) {
	docs := &hookDocuments{map[string]json.RawMessage{}, map[string]int{}}
	svc := eventsource.New(docs, config.SeverityConfig{})
	spec := config.EventSource{EventSourceID: "source", Enabled: true, Storage: config.EventSourceStorageConfig{Type: "kafka", Kafka: config.KafkaStorageConfig{Brokers: []string{"kafka:9092"}, Topic: "raw", ConsumerGroup: "review"}}, Hooks: []config.HookConfig{{Name: "index", Type: config.HookTypeActiveAlertByStrategy, Config: config.HookParameters{Redis: &config.RedisConfig{Address: "redis:6379", Password: "******"}, KeyPrefix: "review"}}}}.WithDefaults()
	api := (&API{Sources: svc, Config: config.DispatchConfig{APIToken: "synthetic-admin"}}).Handler()
	body, err := json.Marshal(Mutation{Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/v1/event-sources/source", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer synthetic-admin")
	rr := httptest.NewRecorder()
	api.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest || len(docs.data) != 0 {
		t.Fatal("masked credential was published")
	}
}
