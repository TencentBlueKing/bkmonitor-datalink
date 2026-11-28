// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"linkd/internal/cleaner"
	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/domain"
	"linkd/internal/eventsource"
	"linkd/internal/logging"
	"linkd/internal/pluginruntime"
	"linkd/internal/store/memory"
)

// TestPythonKafkaIntegration 仅在显式配置时创建独立测试 topic，验证真实 Python→Kafka 链路。
func TestPythonKafkaIntegration(t *testing.T) {
	brokers, python := os.Getenv("LINKD_TEST_KAFKA_BROKERS"), os.Getenv("LINKD_RUNTIME_PYTHON")
	if brokers == "" || python == "" {
		t.Skip("set LINKD_TEST_KAFKA_BROKERS and LINKD_RUNTIME_PYTHON")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	client, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(brokers, ",")...), kgo.RequiredAcks(kgo.AllISRAcks()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	topic := fmt.Sprintf("linkd-collector-integration-%d-%d", os.Getpid(), time.Now().UnixNano())
	create := kmsg.NewPtrCreateTopicsRequest()
	create.Topics = []kmsg.CreateTopicsRequestTopic{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}}
	created, err := create.RequestWith(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Topics) != 1 || created.Topics[0].ErrorCode != 0 {
		t.Fatal("test topic creation failed")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		deletion := kmsg.NewPtrDeleteTopicsRequest()
		deletion.TopicNames = []string{topic}
		deletion.Topics = []kmsg.DeleteTopicsRequestTopic{{Topic: &topic}}
		response, err := deletion.RequestWith(cleanup, client)
		if err != nil {
			t.Error(err)
			return
		}
		for _, result := range response.Topics {
			if result.ErrorCode != 0 {
				t.Errorf("test topic cleanup: %d", result.ErrorCode)
			}
		}
	}()
	folder := t.TempDir()
	tokenFile := filepath.Join(folder, "token")
	if err = os.WriteFile(tokenFile, []byte("integration-token"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../../python")
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // 显式测试解释器，固定服务参数，凭据文件由测试创建。
	command := exec.CommandContext(ctx, python, "-S", "-m", "linkd_runtime", "serve", "--port", "0", "--processes", "1", "--token-file", tokenFile)
	command.Env = append(os.Environ(), "PYTHONPATH="+root)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Signal(syscall.SIGTERM)
		finished := make(chan error, 1)
		go func() { finished <- command.Wait() }()
		select {
		case err := <-finished:
			if err != nil {
				t.Errorf("Python shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-finished
			t.Error("Python shutdown timeout")
		}
	}()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatal("Python startup failed")
	}
	var address struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err = json.Unmarshal(scanner.Bytes(), &address); err != nil {
		t.Fatal(err)
	}
	runtime, err := pluginruntime.NewHTTPClient(&http.Client{Timeout: 5 * time.Second}, fmt.Sprintf("http://%s:%d/execute", address.Host, address.Port), "integration-token", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	service := New(runtime, NewKafkaPublisher(client, topic), nil)
	request := pluginruntime.Request{RequestID: "receipt-1", TenantID: "tenant", SourceID: "source", Plugin: "rest_api_push", Mode: "push", ReceivedAt: time.Now().UTC().Truncate(time.Millisecond).Format(time.RFC3339Nano), Data: json.RawMessage(`{"fingerprint":"alert-1","title":"测试告警","bk_tenant_id":"untrusted","evaluations":[{"action":"triggered","severity":"warning"}]}`)}
	sources := sourceList{{Enabled: true, Secret: "source-secret", Request: request, Service: service}}
	receipts := receiptFunc(func(_ context.Context, accepted pluginruntime.Request) (pluginruntime.Request, error) {
		return accepted, nil
	})
	logger, err := logging.New(logging.DefaultConfig(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := NewReceiver(NewSourceIntake(sources, receipts), 1<<20, 4, logger)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("POST /sources/{source_id}", receiver)
	ingress := httptest.NewServer(mux)
	defer ingress.Close()
	incoming, err := http.NewRequestWithContext(ctx, http.MethodPost, ingress.URL+"/sources/source", strings.NewReader(string(request.Data)))
	if err != nil {
		t.Fatal(err)
	}
	incoming.Header.Set("X-Secret", "source-secret")
	response, err := ingress.Client().Do(incoming)
	if err != nil {
		t.Fatal(err)
	}
	var ack struct {
		Result bool `json:"result"`
		Data   struct {
			Count int `json:"received_alarm_count"`
		} `json:"data"`
	}
	err = json.NewDecoder(response.Body).Decode(&ack)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !ack.Result || ack.Data.Count != 1 {
		t.Fatalf("unexpected ingest response: %d %+v", response.StatusCode, ack)
	}
	consumer, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(brokers, ",")...), kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{topic: {0: kgo.NewOffset().AtStart()}}))
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	fetched := consumer.PollRecords(ctx, 1)
	if err = fetched.Err(); err != nil {
		t.Fatal(err)
	}
	records := fetched.Records()
	if len(records) != 1 {
		t.Fatalf("expected one event, got %d", len(records))
	}
	record := records[0]
	var event map[string]any
	if err = json.Unmarshal(record.Value, &event); err != nil {
		t.Fatal(err)
	}
	if event["bk_tenant_id"] != "tenant" || event["fingerprint"] != "alert-1" || string(record.Key) != "tenant:alert-1" {
		t.Fatal("Kafka event identity mismatch")
	}
	expected, _ := time.Parse(time.RFC3339Nano, request.ReceivedAt)
	if !record.Timestamp.Equal(expected) {
		t.Fatal("Kafka timestamp changed")
	}
	if len(record.Headers) != 2 || record.Headers[0].Key != "bk_tenant_id" || string(record.Headers[0].Value) != "tenant" {
		t.Fatal("Kafka tenant header missing")
	}
	// 首次 Kafka 实际写入成功后模拟 ACK 丢失；第二次发布必须使用缓存结果。
	calls := 0
	attempt := 0
	writer := NewKafkaPublisher(client, topic)
	countedExecutor := executorFunc(func(callCtx context.Context, r pluginruntime.Request) (pluginruntime.Result, error) {
		calls++
		return runtime.Execute(callCtx, r)
	})
	replay := New(countedExecutor, publisherFunc(func(callCtx context.Context, r pluginruntime.Request, result pluginruntime.Result) error {
		if err := writer.Publish(callCtx, r, result); err != nil {
			return err
		}
		attempt++
		if attempt == 1 {
			return errors.New("simulated lost ACK")
		}
		return nil
	}), nil)
	replay.results = &resultFixture{}
	var persistent *RedisReceipts
	if os.Getenv("LINKD_RECEIPTS_INTEGRATION") == "1" {
		persistent = NewRedisReceipts(startReceiptsRedis(t), "python-kafka", 0)
		replay.results = persistent
	}
	request.RequestID = "receipt-retry"
	request.SourceVersion = 3
	if persistent != nil {
		request, err = persistent.Accept(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = replay.Run(ctx, request); err == nil {
		t.Fatal("lost ACK should fail")
	}
	if persistent != nil {
		// 重建 Service/Factory，只从 Redis 发现原输入及结果，使用历史发布恢复到原 topic。
		restored := NewRedisReceipts(persistent.client, "python-kafka", 0)
		factory := NewFactory(countedExecutor, restored)
		defer factory.Close()
		releases := &releaseStub{record: eventsource.Record{Published: 9}, release: eventsource.Release{ID: "source", Version: 3, Spec: config.EventSource{EventSourceID: "source", RelatedTenantID: "tenant", Storage: config.EventSourceStorageConfig{Type: "kafka", Kafka: config.KafkaStorageConfig{Brokers: strings.Split(brokers, ","), Topic: topic}}}}}
		sources := NewPublishedSources(releases, factory.Build)
		recovery := PushRecovery{Pending: restored, Replay: sources.Replay, BatchSize: 1, Timeout: 10 * time.Second, Logger: logger}
		if err = recovery.Step(ctx); err != nil {
			t.Fatal(err)
		}
		if releases.version != 3 {
			t.Fatal("recovery selected latest release")
		}
		outcome, err := restored.LoadResult(ctx, request)
		if err != nil || outcome == nil || !outcome.Completed {
			t.Fatalf("recovery not complete: %+v %v", outcome, err)
		}
		if ids, err := restored.PendingIDs(ctx, "tenant", "source", 1); err != nil || len(ids) != 0 {
			t.Fatalf("pending recovery remains: %v %v", ids, err)
		}
	} else if _, err = replay.Run(ctx, request); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("retry recomputed plugin result")
	}
	var duplicateRecords []*kgo.Record
	for len(duplicateRecords) < 2 {
		fetched := consumer.PollRecords(ctx, 2-len(duplicateRecords))
		if err := fetched.Err(); err != nil {
			t.Fatal(err)
		}
		duplicateRecords = append(duplicateRecords, fetched.Records()...)
	}
	mapper, err := cleaner.NewMapper(config.EventSource{EventSourceID: "source", RelatedTenantID: "tenant", Enabled: true, Storage: config.EventSourceStorageConfig{Type: "kafka", Kafka: config.KafkaStorageConfig{Brokers: strings.Split(brokers, ","), Topic: topic, ConsumerGroup: "test"}}}.WithDefaults(), config.DefaultSeverityConfig())
	if err != nil {
		t.Fatal(err)
	}
	events := make([]domain.Event, 0, 2)
	for _, record := range duplicateRecords {
		headers := map[string][]byte{}
		for _, header := range record.Headers {
			headers[header.Key] = header.Value
		}
		event, err := mapper.MapMessage(ctx, consume.Message{ID: string(headers["message_id"]), TenantID: string(headers["bk_tenant_id"]), Body: record.Value, EnqueuedAt: record.Timestamp, Headers: headers})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if events[0].EventID != events[1].EventID {
		t.Fatal("republication changed Cleaner EventID")
	}
	repository := memory.New()
	stored, err := repository.CreateEvents(ctx, events)
	if err != nil {
		t.Fatal(err)
	}
	if stored[0].Err != nil || stored[1].Err != nil || !stored[0].Result.Created || stored[1].Result.Created {
		t.Fatalf("duplicate Event persisted: %+v", stored)
	}
}
