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
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"linkd/internal/config"
	"linkd/internal/eventgen"
	eventgenstore "linkd/internal/eventgen/storage"
	"linkd/internal/eventsource"
)

type generatorIntegrationSources struct{ source config.EventSource }

func (s generatorIntegrationSources) Get(context.Context, string) (eventsource.Record, error) {
	return eventsource.Record{ID: s.source.EventSourceID, Published: 1}, nil
}

func (s generatorIntegrationSources) GetRelease(context.Context, string, int64) (eventsource.Release, error) {
	return eventsource.Release{Spec: s.source}, nil
}

func TestManagedEventgenKafkaAndPersistenceIntegration(t *testing.T) {
	endpoint, broker := os.Getenv("LINKD_TEST_ELASTICSEARCH_URL"), os.Getenv("LINKD_TEST_KAFKA_BROKER")
	if endpoint == "" || broker == "" {
		t.Skip("set explicit Elasticsearch and Kafka test endpoints")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	id := eventgen.NewRunID()
	topic := "linkd-eventgen-managed-" + id
	admin, err := kgo.NewClient(kgo.SeedBrokers(broker))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	create := kmsg.NewPtrCreateTopicsRequest()
	create.Topics = []kmsg.CreateTopicsRequestTopic{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}}
	response, err := admin.Request(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	if response.(*kmsg.CreateTopicsResponse).Topics[0].ErrorCode != 0 {
		t.Fatal("topic create failed")
	}
	defer func() {
		request := kmsg.NewPtrDeleteTopicsRequest()
		request.TopicNames = []string{topic}
		request.Topics = []kmsg.DeleteTopicsRequestTopic{{Topic: &topic}}
		if _, err := admin.Request(context.WithoutCancel(ctx), request); err != nil {
			t.Error(err)
		}
	}()
	storage, err := eventgenstore.Open(ctx, config.StorageConfig{Repository: "elasticsearch", Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{endpoint}}}, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storage.Close() }()
	defer func() {
		client := http.Client{Timeout: 5 * time.Second}
		for _, kind := range []string{"records", "runs"} {
			//nolint:gosec // G704: 测试端点由显式环境变量指定，只删除本测试创建的独立索引。
			req, err := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodDelete, endpoint+"/linkd_eventgen_"+hex.EncodeToString([]byte(id))+"_"+kind, nil)
			if err != nil {
				t.Error(err)
				continue
			}
			//nolint:gosec // G704: 端点和索引来自上面的隔离测试范围，不接收远端 URL。
			response, err := client.Do(req)
			if err != nil {
				t.Error(err)
				continue
			}
			_ = response.Body.Close()
		}
	}()
	source := config.EventSource{EventSourceID: "managed-source", Enabled: true, Storage: config.EventSourceStorageConfig{Type: "kafka", Kafka: config.KafkaStorageConfig{Brokers: []string{broker}, Topic: topic, ConsumerGroup: "test"}}}
	service := eventgen.NewService(storage, generatorIntegrationSources{source})
	handler := (&API{Eventgen: service, Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "test-admin"}, WorkerToken: "test-worker"}}).Handler()
	registered := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
		if r.URL.Path == "/internal/eventgen/heartbeat" {
			once.Do(func() { close(registered) })
		}
	}))
	defer server.Close()
	workerCtx, stopWorker := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- eventgen.RunManaged(workerCtx, eventgen.ManagedOptions{URL: server.URL, Token: "test-worker", PollInterval: 50 * time.Millisecond})
	}()
	defer func() {
		stopWorker()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("managed worker did not exit")
		}
	}()
	select {
	case <-registered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	c := eventgen.Config{Name: "managed", TenantID: "tenant-test", Seed: 1, Templates: map[string]eventgen.Template{"fire": {Kind: "standard", Body: `{"title":"test","evaluations":[{"severity":"warning","action":"triggered"}]}`}, "end": {Kind: "standard", Body: `{"evaluations":[{"severity":"warning","action":"resolved"}]}`}}, Scenarios: []eventgen.Scenario{{ID: "host", SourceID: source.EventSourceID, Arrival: eventgen.Arrival{PerSecond: 10, Count: 2}, Stages: []eventgen.Stage{{Name: "active", Template: "fire", Duration: eventgen.Span{Fixed: "10ms"}}, {Name: "end", Template: "end", Terminal: true}}}}}
	call := func(method, path string, body any) json.RawMessage {
		t.Helper()
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Internal-Token", testJWT(t, "test-admin"))
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 {
			t.Fatalf("HTTP %d: %s", response.StatusCode, payload)
		}
		return payload
	}
	call(http.MethodPut, "/api/v1/eventgen/tasks/test?bk_tenant_id=tenant-test", map[string]any{"revision": 0, "config": c})
	runID := eventgen.NewRunID()
	call(http.MethodPost, "/api/v1/eventgen/tasks/test/start?bk_tenant_id=tenant-test", eventgen.Command{OperationID: runID, Reason: "integration"})
	for {
		raw := call(http.MethodGet, "/api/v1/eventgen/runs/"+runID+"?bk_tenant_id=tenant-test", nil)
		var run eventgen.Run
		if err = json.Unmarshal(raw, &run); err != nil {
			t.Fatal(err)
		}
		if run.Status.State == "completed" {
			if run.Status.Sent != 4 || run.Status.Failed != 0 || run.Status.Revision != 1 {
				t.Fatalf("unexpected managed result: %+v", run.Status)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
