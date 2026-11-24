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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"linkd/internal/pluginruntime"
)

type producer interface {
	ProduceSync(context.Context, ...*kgo.Record) kgo.ProduceResults
}

// 已知不可重试的协议/消息错误立即终结，未知传输错误仍受持久发布预算限制。
type permanentPublicationError struct{ cause error }

func (e *permanentPublicationError) Error() string { return e.cause.Error() }

func (e *permanentPublicationError) Unwrap() error { return e.cause }

// KafkaPublisher 将 standard 输出投递到已配置来源 topic；连接由宿主创建和关闭。
type KafkaPublisher struct {
	producer producer
	topic    string
}

// NewKafkaPublisher 使用宿主注入的 producer；调用方须配置 AllISRAcks 和投递超时。
func NewKafkaPublisher(client *kgo.Client, topic string) *KafkaPublisher {
	return &KafkaPublisher{producer: client, topic: topic}
}

// Publish 保留固定接收时间，按租户和 fingerprint 分区，并等待每条消息 ACK。
// 部分 ACK 后失败不会回滚 Kafka；跨调用重试仍可能重复写入，不宣称恰好一次投递。
func (p *KafkaPublisher) Publish(ctx context.Context, request pluginruntime.Request, result pluginruntime.Result) error {
	if request.RequestID == "" {
		return &permanentPublicationError{fmt.Errorf("source request ID is required for publication")}
	}
	received, err := time.Parse(time.RFC3339Nano, request.ReceivedAt)
	if err != nil {
		return &permanentPublicationError{fmt.Errorf("parse source received_at: %w", err)}
	}
	records := make([]*kgo.Record, 0, len(result.Events))
	for index, body := range result.Events {
		var event struct {
			Fingerprint string `json:"fingerprint"`
		}
		if err = json.Unmarshal(body, &event); err != nil {
			return &permanentPublicationError{fmt.Errorf("decode event partition key: %w", err)}
		}
		records = append(records, &kgo.Record{Topic: p.topic, Key: []byte(request.TenantID + ":" + event.Fingerprint), Value: body, Timestamp: received, Headers: []kgo.RecordHeader{{Key: "bk_tenant_id", Value: []byte(request.TenantID)}, {Key: "message_id", Value: []byte(fmt.Sprintf("collector/%s/%d", request.RequestID, index))}}})
	}
	if err = p.producer.ProduceSync(ctx, records...).FirstErr(); err != nil {
		var brokerError *kerr.Error
		if errors.As(err, &brokerError) && !brokerError.Retriable {
			return &permanentPublicationError{fmt.Errorf("publish plugin batch: %w", err)}
		}
		return fmt.Errorf("publish plugin batch: %w", err)
	}
	return nil
}
