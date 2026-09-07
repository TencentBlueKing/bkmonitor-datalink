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
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"linkd/internal/config"
	"linkd/internal/kafkaclient"
)

// Probe 查询完整 topic 分片集合，显式禁止自动创建 topic。
func Probe(ctx context.Context, s config.EventSource) (string, int, error) {
	options, e := kafkaclient.ClientOptions(s.Storage.Kafka.Brokers, "linkd-source-probe", s.Storage.Kafka.Security)
	if e != nil {
		return "", 0, e
	}
	client, e := kgo.NewClient(options...)
	if e != nil {
		return "", 0, e
	}
	defer client.Close()
	request := kmsg.NewPtrMetadataRequest()
	request.AllowAutoTopicCreation = false
	name := s.Storage.Kafka.Topic
	request.Topics = []kmsg.MetadataRequestTopic{{Topic: &name}}
	response, e := request.RequestWith(ctx, client)
	if e != nil {
		return "", 0, fmt.Errorf("kafka metadata unavailable")
	}
	if len(response.Topics) != 1 {
		return "", 0, fmt.Errorf("incomplete topic metadata")
	}
	topic := response.Topics[0]
	if topic.ErrorCode != 0 || topic.Topic == nil || *topic.Topic != name || len(topic.Partitions) == 0 {
		return "", 0, fmt.Errorf("topic metadata error %d", topic.ErrorCode)
	}
	seen := map[int32]bool{}
	for _, p := range topic.Partitions {
		if p.Partition < 0 || seen[p.Partition] {
			return "", 0, fmt.Errorf("incomplete partition metadata")
		}
		seen[p.Partition] = true
		if p.ErrorCode != 0 && p.ErrorCode != 5 && p.ErrorCode != 6 {
			return "", 0, fmt.Errorf("partition metadata error %d", p.ErrorCode)
		}
	}
	for i := 0; i < len(seen); i++ {
		if !seen[int32(i)] {
			return "", 0, fmt.Errorf("partition metadata contains gaps")
		}
	}
	return hex.EncodeToString(topic.TopicID[:]), len(seen), nil
}

// UpdateMetadata 不让临时 leader/ISR 故障和失败响应触发缩容。
func UpdateMetadata(previous Metadata, s config.EventSource, id string, count int, err error, now time.Time) Metadata {
	d := digest(s.Storage)
	if previous.Digest != d {
		previous = Metadata{Digest: d}
	}
	previous.Attempt = now
	if err != nil {
		previous.Error = err.Error()
		return previous
	}
	if previous.Partitions > 0 && (count < previous.Partitions || (previous.TopicID != "" && id != previous.TopicID)) {
		previous.Error = "topic identity changed or partition count decreased"
		return previous
	}
	previous.TopicID = id
	previous.Partitions = count
	previous.Success = now
	previous.Error = ""
	return previous
}
