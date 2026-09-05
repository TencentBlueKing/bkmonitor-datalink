// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"linkd/internal/consume"
)

// TestKafkaPartitionTailFetchWait 使用独立三分区 topic、已停止的有限生产者，
// 对照同一分区的 pause/resume 空档。不是 ES/Lifecycle 吞吐压测。
func TestKafkaPartitionTailFetchWait(t *testing.T) {
	brokers := os.Getenv("LINKD_TEST_KAFKA_BROKERS")
	if brokers == "" {
		t.Skip("set LINKD_TEST_KAFKA_BROKERS for Kafka integration")
	}
	for _, wait := range []time.Duration{5 * time.Second, 100 * time.Millisecond} {
		t.Run(wait.String(), func(t *testing.T) { testPartitionTail(t, strings.Split(brokers, ","), wait) })
	}
}

func testPartitionTail(t *testing.T, brokers []string, wait time.Duration) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	topic := fmt.Sprintf("linkd-tail-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	admin, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	metadata, err := kmsg.NewPtrMetadataRequest().RequestWith(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Brokers) == 0 {
		t.Fatal("no Kafka broker")
	}
	// 必须共享同一 broker fetch 循环，避免多 broker 分配掩盖本次空分区等待。
	assignments := make([]kmsg.CreateTopicsRequestTopicReplicaAssignment, 3)
	for i := range assignments {
		assignments[i] = kmsg.CreateTopicsRequestTopicReplicaAssignment{Partition: int32(i), Replicas: []int32{metadata.Brokers[0].NodeID}}
	}
	create := kmsg.NewPtrCreateTopicsRequest()
	create.Topics = []kmsg.CreateTopicsRequestTopic{{Topic: topic, NumPartitions: -1, ReplicationFactor: -1, ReplicaAssignment: assignments}}
	created, err := create.RequestWith(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range created.Topics {
		if r.ErrorCode != 0 {
			t.Fatalf("create topic: %d", r.ErrorCode)
		}
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		request := kmsg.NewPtrDeleteTopicsRequest()
		request.TopicNames = []string{topic}
		request.Topics = []kmsg.DeleteTopicsRequestTopic{{Topic: &topic}}
		response, e := request.RequestWith(cleanup, admin)
		if e != nil {
			t.Errorf("cleanup topic %s: %v", topic, e)
			return
		}
		if len(response.Topics) != 1 {
			t.Errorf("cleanup topic returned %d results", len(response.Topics))
		}
		for _, r := range response.Topics {
			if r.ErrorCode != 0 {
				t.Errorf("cleanup topic: %d", r.ErrorCode)
			}
		}
		groups := kmsg.NewPtrDeleteGroupsRequest()
		groups.Groups = []string{topic}
		deleted, e := groups.RequestWith(cleanup, admin)
		if e != nil {
			t.Errorf("cleanup group: %v", e)
			return
		}
		if len(deleted.Groups) != 1 {
			t.Errorf("cleanup group returned %d results", len(deleted.Groups))
		}
		for _, r := range deleted.Groups {
			if r.ErrorCode != 0 && r.ErrorCode != 69 {
				t.Errorf("cleanup group: %d", r.ErrorCode)
			}
		}
	}()
	producer, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.RecordPartitioner(kgo.ManualPartitioner()))
	if err != nil {
		t.Fatal(err)
	}
	const total = 1024
	records := make([]*kgo.Record, total)
	for i := range records {
		records[i] = &kgo.Record{Topic: topic, Partition: 1, Key: []byte("same-partition"), Value: []byte(strconv.Itoa(i))}
	}
	result := producer.ProduceSync(ctx, records...)
	producer.Close() // 消费开始前停止输入，不允许其他分区的数据唤醒长轮询。
	if err := result.FirstErr(); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Brokers: brokers, Topic: topic, ConsumerGroup: topic, FetchMaxWait: wait})
	if err != nil {
		t.Fatal(err)
	}
	eventsCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	go func() {
		for {
			select {
			case <-eventsCtx.Done():
				return
			case e := <-s.OwnershipEvents():
				e.Complete()
			}
		}
	}()
	defer func() {
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	received := 0
	var started time.Time
	var resumed time.Time
	var gaps []time.Duration
	for received < total {
		deliveries, err := s.Receive(ctx, consume.ReceiveLimits{MaxMessages: 256, MaxBytes: 4 << 20})
		if err != nil {
			t.Fatal(err)
		}
		if len(deliveries) == 0 {
			continue
		}
		if !resumed.IsZero() {
			gaps = append(gaps, time.Since(resumed))
			resumed = time.Time{}
		}
		if started.IsZero() {
			started = time.Now()
		}
		receipts := make([]consume.Receipt, len(deliveries))
		for i, d := range deliveries {
			if string(d.Message.Body) != strconv.Itoa(received) || d.Meta.Lane != topic+"/1" {
				t.Fatalf("unexpected offset/order at %d", received)
			}
			receipts[i] = d.Receipt
			received++
		}
		if err := s.Confirm(ctx, receipts); err != nil {
			t.Fatal(err)
		}
		if received >= total {
			break
		}
		if err := s.Pause(ctx, topic+"/1"); err != nil {
			t.Fatal(err)
		}
		// Poll 清除暂停分区已预取的缓冲，并让其他空分区发起 fetch。
		pausedCtx, stop := context.WithTimeout(ctx, 200*time.Millisecond)
		for pausedCtx.Err() == nil {
			paused, pausedErr := s.Receive(pausedCtx, consume.ReceiveLimits{MaxMessages: 256, MaxBytes: 4 << 20})
			if len(paused) != 0 {
				t.Fatal("paused partition was delivered")
			}
			if pausedErr != nil && pausedCtx.Err() == nil {
				t.Fatal(pausedErr)
			}
			select {
			case <-pausedCtx.Done():
			case <-time.After(10 * time.Millisecond):
			}
		}
		stop()
		resumed = time.Now()
		if err := s.Resume(ctx, topic+"/1"); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("fetch_wait=%s records=%d elapsed=%s resume_gaps=%v", wait, received, time.Since(started), gaps)
	if len(gaps) != 3 {
		t.Fatal("pause/resume path not exercised")
	}
	if wait == 5*time.Second {
		for _, gap := range gaps {
			if gap < time.Second {
				t.Errorf("baseline long-fetch gap not reproduced: %s", gap)
			}
		}
	}
	if wait == 100*time.Millisecond {
		for _, gap := range gaps {
			if gap > 2*time.Second {
				t.Errorf("resume gap still exceeds 2s: %s", gap)
			}
		}
	}
}
