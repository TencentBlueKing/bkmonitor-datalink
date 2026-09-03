// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventgen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"linkd/internal/cleaner"
	linkdconfig "linkd/internal/config"
	"linkd/internal/domain"
)

const kafkaIntegrationBrokerEnv = "LINKD_TEST_KAFKA_BROKER"

func TestKafkaPublisherIntegration(t *testing.T) {
	broker := os.Getenv(kafkaIntegrationBrokerEnv)
	if broker == "" {
		t.Skipf("set %s to run the real Kafka eventgen integration test", kafkaIntegrationBrokerEnv)
	}
	runID, err := NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	topic := "linkd-eventgen-test-" + runID
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	admin, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ClientID("linkd-eventgen-test-admin"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	createKafkaIntegrationTopic(ctx, t, admin, topic)
	defer deleteKafkaIntegrationTopic(t, admin, topic)

	source := testSource()
	source.Storage.Kafka.Brokers = []string{broker}
	source.Storage.Kafka.Topic = topic
	publisher, err := NewKafkaPublisher(source)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	engine, err := New(Config{
		RunID: runID, TenantID: "tenant-integration",
		NewAlertsPerMinute: 6000, CycleDuration: 100 * time.Millisecond, MeanLifetimeCycles: 1,
		Scenarios: []Scenario{ScenarioCPUHigh, ScenarioDiskFull},
		Seed:      42, MaxActiveAlerts: 100, Cycles: 2,
	}, source, linkdconfig.DefaultSeverityConfig(), publisher, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	if _, err := engine.RunCycle(ctx, 1, startedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunCycle(ctx, 2, startedAt.Add(100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	consumer, err := kgo.NewClient(
		kgo.SeedBrokers(broker),
		kgo.ConsumerGroup("linkd-eventgen-test-"+runID),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	actions := make(map[domain.EventAction]int)
	received := 0
	for received < 30 {
		fetches := consumer.PollFetches(ctx)
		if err := fetches.Err(); err != nil {
			t.Fatalf("consume eventgen records: %v", err)
		}
		for _, record := range fetches.Records() {
			if len(record.Key) == 0 {
				t.Fatal("eventgen Kafka key is empty")
			}
			headers := kafkaHeaderMap(record.Headers)
			for _, key := range []string{"message_id", "bk_tenant_id", "order_key"} {
				if headers[key] == "" {
					t.Fatalf("eventgen Kafka record missing header %q", key)
				}
			}
			draft, cleanErr := (cleaner.StandardCleaner{}).Clean(ctx, cleaner.RawEventMessage{Payload: record.Value})
			if cleanErr != nil {
				t.Fatalf("decode eventgen Kafka record: %v", cleanErr)
			}
			actions[draft.Action]++
			received++
		}
	}
	if actions[domain.EventActionTriggered] != 20 || actions[domain.EventActionResolved] != 10 {
		t.Fatalf("Kafka action counts = %#v", actions)
	}
}

func createKafkaIntegrationTopic(ctx context.Context, t *testing.T, client *kgo.Client, topic string) {
	t.Helper()
	request := kmsg.NewPtrCreateTopicsRequest()
	request.Topics = append(request.Topics, kmsg.CreateTopicsRequestTopic{
		Topic: topic, NumPartitions: 3, ReplicationFactor: 1,
	})
	response, err := client.Request(ctx, request)
	if err != nil {
		t.Fatalf("create Kafka integration topic: %v", err)
	}
	created, ok := response.(*kmsg.CreateTopicsResponse)
	if !ok || len(created.Topics) != 1 {
		t.Fatalf("create Kafka integration topic returned %T: %#v", response, response)
	}
	if created.Topics[0].ErrorCode != 0 {
		t.Fatalf("create Kafka integration topic: %v", kerr.ErrorForCode(created.Topics[0].ErrorCode))
	}
}

func deleteKafkaIntegrationTopic(t *testing.T, client *kgo.Client, topic string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request := kmsg.NewPtrDeleteTopicsRequest()
	request.TopicNames = []string{topic}
	request.Topics = []kmsg.DeleteTopicsRequestTopic{{Topic: &topic}}
	response, err := client.Request(ctx, request)
	if err != nil {
		t.Errorf("delete Kafka integration topic: %v", err)
		return
	}
	deleted, ok := response.(*kmsg.DeleteTopicsResponse)
	if !ok || len(deleted.Topics) != 1 {
		t.Errorf("delete Kafka integration topic returned %T: %#v", response, response)
		return
	}
	if deleted.Topics[0].ErrorCode != 0 &&
		!errors.Is(kerr.ErrorForCode(deleted.Topics[0].ErrorCode), kerr.UnknownTopicOrPartition) {
		t.Errorf("delete Kafka integration topic %q: %v", topic, kerr.ErrorForCode(deleted.Topics[0].ErrorCode))
	}
}

func kafkaHeaderMap(headers []kgo.RecordHeader) map[string]string {
	result := make(map[string]string, len(headers))
	for _, header := range headers {
		if _, exists := result[header.Key]; exists {
			result[header.Key] = fmt.Sprintf("<duplicate:%s>", header.Key)
			continue
		}
		result[header.Key] = string(header.Value)
	}
	return result
}
