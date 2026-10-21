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
	"runtime"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"linkd/internal/cleaner"
	"linkd/internal/config"
)

func integrationKafka(t *testing.T) (string, string, *kgo.Client) {
	t.Helper()
	broker := os.Getenv("LINKD_TEST_KAFKA_BROKER")
	if broker == "" {
		t.Skip("set LINKD_TEST_KAFKA_BROKER to run real Kafka tests")
	}
	client, err := kgo.NewClient(kgo.SeedBrokers(broker))
	if err != nil {
		t.Fatal(err)
	}
	topic := "linkd-eventgen-test-" + NewRunID()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	createKafkaIntegrationTopic(ctx, t, client, topic)
	t.Cleanup(func() { deleteKafkaIntegrationTopic(t, client, topic); client.Close() })
	return broker, topic, client
}

func kafkaSource(broker, topic string) map[string]config.EventSource {
	return map[string]config.EventSource{"source": {EventSourceID: "source", Enabled: true, Storage: config.EventSourceStorageConfig{Type: "kafka", Kafka: config.KafkaStorageConfig{Brokers: []string{broker}, Topic: topic}}}}
}

func TestKafkaPublisherIntegration(t *testing.T) {
	broker, topic, _ := integrationKafka(t)
	c := fixture()
	records, err := Preview(c, startTime(), 10)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := NewKafkaPublisher(kafkaSource(broker, topic), c.Limits)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	for _, err := range publisher.Publish(ctx, records) {
		if err != nil {
			t.Fatal(err)
		}
	}
	consumer, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{topic: {0: kgo.NewOffset().AtStart(), 1: kgo.NewOffset().AtStart(), 2: kgo.NewOffset().AtStart()}}))
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	received := 0
	for received < len(records) {
		fetches := consumer.PollFetches(ctx)
		if err := fetches.Err(); err != nil {
			t.Fatal(err)
		}
		for _, r := range fetches.Records() {
			h := kafkaHeaderMap(r.Headers)
			if h["bk_tenant_id"] != c.TenantID || h["message_id"] == "" {
				t.Fatal("missing identity headers")
			}
			if _, err := (cleaner.StandardCleaner{}).Clean(ctx, cleaner.RawEventMessage{Payload: r.Value}); err != nil {
				t.Fatal(err)
			}
			received++
		}
	}
}

// TestKafkaLoad 明确启用时依次执行三档真实 Kafka 压测；日志只保存指标，不输出业务数据。
func TestKafkaLoad(t *testing.T) {
	seconds, _ := strconv.Atoi(os.Getenv("LINKD_EVENTGEN_LOAD_SECONDS"))
	if seconds <= 0 {
		t.Skip("set LINKD_EVENTGEN_LOAD_SECONDS=600 for the acceptance load test")
	}
	for _, eps := range []int{1000, 5000, 10000} {
		t.Run(strconv.Itoa(eps), func(t *testing.T) {
			broker, topic, _ := integrationKafka(t)
			c := fixture()
			c.Duration = Duration(fmt.Sprintf("%ds", seconds))
			c.Scenarios[0].Arrival = Arrival{PerSecond: float64(eps) / 2}
			c.Scenarios[0].Stages[0].Duration = Span{Fixed: "1s"}
			c.Scenarios[0].Stages[0].Every = Span{}
			c.Limits.Samples = 1
			publisher, err := NewKafkaPublisher(kafkaSource(broker, topic), c.Limits)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Duration(seconds+60)*time.Second)
			defer cancel()
			engine, err := New(c, NewRunID(), 1, time.Now(), publisher)
			if err != nil {
				t.Fatal(err)
			}
			consumer, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{topic: {0: kgo.NewOffset().AtStart(), 1: kgo.NewOffset().AtStart(), 2: kgo.NewOffset().AtStart()}}))
			if err != nil {
				t.Fatal(err)
			}
			defer consumer.Close()
			var received atomic.Uint64
			consumeCtx, stopConsumer := context.WithCancel(ctx)
			defer stopConsumer()
			consumerDone := make(chan struct{})
			go func() {
				defer close(consumerDone)
				for consumeCtx.Err() == nil {
					fetches := consumer.PollFetches(consumeCtx)
					received.Add(uint64(len(fetches.Records())))
				}
			}()
			done := make(chan error, 1)
			var before, after syscall.Rusage
			if err = syscall.Getrusage(syscall.RUSAGE_SELF, &before); err != nil {
				t.Fatal(err)
			}
			began := time.Now()
			go func() { done <- engine.Run(ctx) }()
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			peak := uint64(0)
			for {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
					deadline := time.Now().Add(10 * time.Second)
					for received.Load() < engine.Snapshot().Sent && time.Now().Before(deadline) {
						time.Sleep(20 * time.Millisecond)
					}
					stopConsumer()
					<-consumerDone
					if err = syscall.Getrusage(syscall.RUSAGE_SELF, &after); err != nil {
						t.Fatal(err)
					}
					status := engine.Snapshot()
					elapsed := time.Since(began).Seconds()
					cpu := float64(after.Utime.Sec-before.Utime.Sec+after.Stime.Sec-before.Stime.Sec) + float64(after.Utime.Usec-before.Utime.Usec+after.Stime.Usec-before.Stime.Usec)/1e6
					t.Logf("target=%d duration=%.3fs sent=%d received=%d failed=%d eps=%.3f peak_heap_bytes=%d cpu_seconds=%.3f queued=%d lag_max_seconds=%.6f", eps, elapsed, status.Sent, received.Load(), status.Failed, float64(status.Sent)/float64(seconds), peak, cpu, status.Queued, status.LagSeconds)
					if status.Failed != 0 || status.Sent != received.Load() || status.Queued != 0 {
						t.Fatal("delivery or drain failed", status)
					}
					actual := float64(status.Sent) / float64(seconds)
					if actual < float64(eps)*.95 || actual > float64(eps)*1.05 {
						t.Fatalf("rate outside 5%%: %.3f", actual)
					}
					return
				case <-ticker.C:
					var memory runtime.MemStats
					runtime.ReadMemStats(&memory)
					peak = max(peak, memory.HeapAlloc)
					s := engine.Snapshot()
					t.Logf("progress target=%d sent=%d received=%d failed=%d active=%d queued=%d heap=%d error=%s", eps, s.Sent, received.Load(), s.Failed, s.Active, s.Queued, memory.HeapAlloc, s.Error)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
		})
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
	// CreateTopics 返回不代表 leader 已可写；测试在压测计时前等待临时 topic 就绪。
	for ctx.Err() == nil {
		metadata := kmsg.NewPtrMetadataRequest()
		metadata.Topics = []kmsg.MetadataRequestTopic{{Topic: &topic}}
		response, err := client.Request(ctx, metadata)
		ready := err == nil
		if err == nil {
			m := response.(*kmsg.MetadataResponse)
			ready = len(m.Topics) == 1 && m.Topics[0].ErrorCode == 0 && len(m.Topics[0].Partitions) == 3
			if ready {
				for _, partition := range m.Topics[0].Partitions {
					if partition.ErrorCode != 0 || partition.Leader < 0 {
						ready = false
					}
				}
			}
		}
		if ready {
			// KRaft 元数据可能先于分区日志就绪；ListOffsets 成功才开始业务发送。
			offsets := kmsg.NewPtrListOffsetsRequest()
			topicOffsets := kmsg.NewListOffsetsRequestTopic()
			topicOffsets.Topic = topic
			for i := range int32(3) {
				partition := kmsg.NewListOffsetsRequestTopicPartition()
				partition.Partition = i
				partition.Timestamp = -1
				topicOffsets.Partitions = append(topicOffsets.Partitions, partition)
			}
			offsets.Topics = append(offsets.Topics, topicOffsets)
			response, err := client.Request(ctx, offsets)
			if err == nil {
				for _, topicResult := range response.(*kmsg.ListOffsetsResponse).Topics {
					for _, partition := range topicResult.Partitions {
						ready = ready && partition.ErrorCode == 0
					}
				}
				if ready {
					return
				}
			}
		}
		if err := waitContext(ctx, 100*time.Millisecond); err != nil {
			break
		}
	}
	t.Fatal("temporary Kafka topic did not become ready")
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
