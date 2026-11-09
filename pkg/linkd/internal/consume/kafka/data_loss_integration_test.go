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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"linkd/internal/consume"
	"linkd/internal/logging"
)

// TestKafkaDataLossRecovery 仅在显式指定回环地址的独立 Kafka 上运行。
// 以专用消费组的超前历史位点触发真实 epoch 校验，不操作其他 topic/group，
// 不声称复现了 Broker 磁盘故障或多副本截断的成因。
func TestKafkaDataLossRecovery(t *testing.T) {
	broker := os.Getenv("LINKD_TEST_KAFKA_DATA_LOSS_BROKER")
	if broker == "" {
		t.Skip("set LINKD_TEST_KAFKA_DATA_LOSS_BROKER to an isolated loopback Kafka")
	}
	host, _, err := net.SplitHostPort(broker)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("data loss integration requires a numeric loopback broker address")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	topic := fmt.Sprintf("linkd-data-loss-%d-%d", os.Getpid(), time.Now().UnixNano())
	admin, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.RecordPartitioner(kgo.ManualPartitioner()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	request := kmsg.NewPtrCreateTopicsRequest()
	request.Topics = []kmsg.CreateTopicsRequestTopic{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}}
	created, err := request.RequestWith(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Topics) != 1 || created.Topics[0].ErrorCode != 0 {
		t.Fatalf("create isolated topic: %+v", created.Topics)
	}
	t.Cleanup(func() { cleanupDataLossResources(t, admin, topic) })
	seed := []*kgo.Record{
		{Topic: topic, Value: []byte("seed-0")},
		{Topic: topic, Value: []byte("seed-1")},
		{Topic: topic, Value: []byte("seed-2")},
	}
	if err := admin.ProduceSync(ctx, seed...).FirstErr(); err != nil {
		t.Fatal(err)
	}
	// 保留真实 leader epoch，但让消费组位点高于日志末尾，构造恢复旧消费进度
	// 与当前日志不一致的状态；ErrDataLoss 由 Kafka/SDK 的真实交互产生。
	commit := kmsg.NewPtrOffsetCommitRequest()
	commit.Group = topic
	partition := kmsg.NewOffsetCommitRequestTopicPartition()
	partition.Offset = seed[2].Offset + 101
	partition.LeaderEpoch = seed[2].LeaderEpoch
	commit.Topics = []kmsg.OffsetCommitRequestTopic{{Topic: topic, Partitions: []kmsg.OffsetCommitRequestTopicPartition{partition}}}
	committed, err := commit.RequestWith(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(committed.Topics) != 1 || len(committed.Topics[0].Partitions) != 1 || committed.Topics[0].Partitions[0].ErrorCode != 0 {
		t.Fatalf("seed historical offset: %+v", committed.Topics)
	}
	var output bytes.Buffer
	logger, err := logging.New(logging.DefaultConfig(), &output)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Brokers: []string{broker}, Topic: topic, ConsumerGroup: topic, ClientID: "linkd-data-loss-test"}
	session, closeSession := openDataLossSession(t, cfg, logger)
	limits := consume.ReceiveLimits{MaxMessages: 10, MaxBytes: 4096}
	deliveries, err := session.Receive(ctx, limits)
	if err != nil || len(deliveries) != 0 {
		t.Fatalf("Receive(data loss) = %d records, %v; warning=%s", len(deliveries), err, output.String())
	}
	var warning struct {
		Level           string `json:"level"`
		ConsumedTo      int64  `json:"consumed_to"`
		ConsumedToEpoch int32  `json:"consumed_to_epoch"`
		ResetTo         int64  `json:"reset_to"`
		Error           string `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &warning); err != nil {
		t.Fatalf("expected actual SDK data loss warning: %v; output=%s", err, output.String())
	}
	if warning.Level != "WARN" || warning.ConsumedTo != partition.Offset || warning.ConsumedToEpoch != partition.LeaderEpoch || warning.ResetTo != seed[2].Offset+1 {
		t.Fatalf("unexpected actual SDK warning: %+v", warning)
	}
	t.Logf("actual Kafka ErrDataLoss: %s", warning.Error)
	if offset := dataLossCommittedOffset(t, ctx, admin, topic); offset != partition.Offset {
		t.Fatalf("notification changed committed offset: %d, want %d", offset, partition.Offset)
	}
	for i := 0; i < 2; i++ {
		marker := &kgo.Record{Topic: topic, Value: []byte(fmt.Sprintf("after-reset-%d", i))}
		if err := admin.ProduceSync(ctx, marker).FirstErr(); err != nil {
			t.Fatal(err)
		}
		confirmDataLossMarker(t, ctx, session, admin, marker)
	}
	closeSession()
	// 重建 Session 后必须读取已确认位点，不能再次从超前历史位点触发同一故障。
	session, _ = openDataLossSession(t, cfg, logger)
	marker := &kgo.Record{Topic: topic, Value: []byte("after-restart")}
	if err := admin.ProduceSync(ctx, marker).FirstErr(); err != nil {
		t.Fatal(err)
	}
	confirmDataLossMarker(t, ctx, session, admin, marker)
	if bytes.Count(output.Bytes(), []byte(`"level":"WARN"`)) != 1 {
		t.Fatalf("data loss warning repeated after recovery: %s", output.String())
	}
	t.Logf("recovered in the same Client, confirmed two new records and one record after Session restart; final committed offset=%d", marker.Offset+1)
}

func openDataLossSession(t *testing.T, cfg Config, logger Logger) (*Session, func()) {
	t.Helper()
	session, err := NewSession(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-session.OwnershipEvents():
				event.Complete()
			}
		}
	}()
	var once sync.Once
	closeSession := func() {
		once.Do(func() {
			cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			if err := session.Close(cleanup); err != nil {
				t.Errorf("close data loss session: %v", err)
			}
			cancel()
			<-done
		})
	}
	t.Cleanup(closeSession)
	return session, closeSession
}

func confirmDataLossMarker(t *testing.T, ctx context.Context, session *Session, admin *kgo.Client, marker *kgo.Record) {
	t.Helper()
	deliveries, err := session.Receive(ctx, consume.ReceiveLimits{MaxMessages: 10, MaxBytes: 4096})
	if err != nil || len(deliveries) != 1 || !bytes.Equal(deliveries[0].Message.Body, marker.Value) {
		t.Fatalf("Receive(marker) = %#v, %v; want %s", deliveries, err, marker.Value)
	}
	if err := session.Confirm(ctx, []consume.Receipt{deliveries[0].Receipt}); err != nil {
		t.Fatal(err)
	}
	if offset := dataLossCommittedOffset(t, ctx, admin, marker.Topic); offset != marker.Offset+1 {
		t.Fatalf("broker committed offset=%d, want %d", offset, marker.Offset+1)
	}
}

func dataLossCommittedOffset(t *testing.T, ctx context.Context, admin *kgo.Client, topic string) int64 {
	t.Helper()
	request := kmsg.NewPtrOffsetFetchRequest()
	request.Group = topic
	request.Topics = []kmsg.OffsetFetchRequestTopic{{Topic: topic, Partitions: []int32{0}}}
	request.Groups = []kmsg.OffsetFetchRequestGroup{{Group: topic, Topics: []kmsg.OffsetFetchRequestGroupTopic{{Topic: topic, Partitions: []int32{0}}}}}
	response, err := request.RequestWith(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Groups) == 1 && response.Groups[0].ErrorCode == 0 && len(response.Groups[0].Topics) == 1 && len(response.Groups[0].Topics[0].Partitions) == 1 {
		partition := response.Groups[0].Topics[0].Partitions[0]
		if partition.ErrorCode == 0 {
			return partition.Offset
		}
	}
	if response.ErrorCode == 0 && len(response.Topics) == 1 && len(response.Topics[0].Partitions) == 1 && response.Topics[0].Partitions[0].ErrorCode == 0 {
		return response.Topics[0].Partitions[0].Offset
	}
	t.Fatalf("fetch committed offset: %+v", response)
	return 0
}

func cleanupDataLossResources(t *testing.T, admin *kgo.Client, topic string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	groups := kmsg.NewPtrDeleteGroupsRequest()
	groups.Groups = []string{topic}
	deleted, err := groups.RequestWith(ctx, admin)
	if err != nil {
		t.Errorf("cleanup isolated group: %v", err)
	} else if len(deleted.Groups) != 1 || deleted.Groups[0].ErrorCode != 0 {
		t.Errorf("cleanup isolated group: %+v", deleted.Groups)
	}
	request := kmsg.NewPtrDeleteTopicsRequest()
	request.TopicNames = []string{topic}
	request.Topics = []kmsg.DeleteTopicsRequestTopic{{Topic: &topic}}
	response, err := request.RequestWith(ctx, admin)
	if err != nil {
		t.Errorf("cleanup isolated topic: %v", err)
	} else if len(response.Topics) != 1 || response.Topics[0].ErrorCode != 0 {
		t.Errorf("cleanup isolated topic: %+v", response.Topics)
	}
}
