// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafkahook

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash"
	"strings"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"linkd/internal/domain"
	"linkd/internal/kafkaclient"
	"linkd/internal/lifecycle"
)

const (
	messageSchemaVersion = "1"
	// 身份哈希域与消息 schema 独立，协议升级不能无条件改变同一业务快照的身份。
	messageIDDomain    = "linkd:alert-message"
	partitionKeyDomain = "linkd:alert-partition"
)

var messageIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

type producer interface {
	ProduceSync(ctx context.Context, records ...*kgo.Record) kgo.ProduceResults
	Close()
}

// Hook 把最终 Alert 快照同步发送到配置的 Kafka topic，并等待 broker ACK。
type Hook struct {
	config    Config
	producer  producer
	closeOnce sync.Once
}

// Message 是发送到 Kafka 的稳定 V1 Alert change 快照信封。
type Message struct {
	MessageID     string                     `json:"message_id"`
	SchemaVersion string                     `json:"schema_version"`
	BKTenantID    string                     `json:"bk_tenant_id"`
	AlertID       string                     `json:"alert_id"`
	UpdateAt      time.Time                  `json:"update_at"`
	Cause         lifecycle.AlertChangeCause `json:"cause"`
	EnrichStatus  domain.EnrichStatus        `json:"enrich_status"`
	Alert         domain.Alert               `json:"alert"`
}

// New 创建拥有独立 franz-go client 的 Kafka FinalHook。
func New(config Config) (*Hook, error) {
	config = config.WithDefaults()
	if err := config.validateStatic(); err != nil {
		return nil, fmt.Errorf("create kafka alert hook: %w", err)
	}
	options, err := kafkaclient.ClientOptions(config.Brokers, config.ClientID, config.Security)
	if err != nil {
		return nil, fmt.Errorf("create kafka alert hook options: %w", err)
	}
	// Config.Validate 已把 MaxMessageBytes 限制在 [1, math.MaxInt32]。
	batchMaxBytes := int32(config.MaxMessageBytes) //nolint:gosec // G115: validated before conversion.
	options = append(
		options,
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchMaxBytes(batchMaxBytes),
	)
	client, err := kgo.NewClient(options...)
	if err != nil {
		return nil, fmt.Errorf("create kafka alert hook client: %w", err)
	}
	return newHook(config, client), nil
}

func newHook(config Config, client producer) *Hook {
	return &Hook{config: config.WithDefaults(), producer: client}
}

// Execute 发送完整 Alert 快照；返回的 MessageID 在所有重试中保持稳定。
func (h *Hook) Execute(
	ctx context.Context,
	input lifecycle.FinalHookInput,
) (lifecycle.FinalHookResult, error) {
	result := lifecycle.FinalHookResult{
		Name:        "alert_kafka",
		Transport:   "kafka",
		Destination: h.config.Topic,
		MessageID:   messageID(input.Alert),
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := validateInput(input); err != nil {
		return result, err
	}
	message := Message{
		MessageID: result.MessageID, SchemaVersion: messageSchemaVersion,
		BKTenantID: input.Alert.BKTenantID, AlertID: input.Alert.AlertID,
		UpdateAt: input.Alert.UpdateAt, Cause: input.Cause,
		EnrichStatus: input.Alert.EnrichStatus, Alert: input.Alert.Clone(),
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return result, fmt.Errorf("marshal kafka alert hook message: %w", err)
	}
	if len(payload) > h.config.MaxMessageBytes {
		return result, fmt.Errorf(
			"kafka alert hook payload exceeds %d bytes: %d",
			h.config.MaxMessageBytes,
			len(payload),
		)
	}
	record := &kgo.Record{
		Topic:     h.config.Topic,
		Key:       []byte(partitionKey(input.Alert)),
		Value:     payload,
		Timestamp: input.Alert.UpdateAt,
		Headers: []kgo.RecordHeader{
			{Key: "message_id", Value: []byte(result.MessageID)},
			{Key: "schema_version", Value: []byte(messageSchemaVersion)},
			{Key: "bk_tenant_id", Value: []byte(input.Alert.BKTenantID)},
			{Key: "alert_id", Value: []byte(input.Alert.AlertID)},
			{Key: "cause_type", Value: []byte(input.Cause.Type)},
			{Key: "cause_id", Value: []byte(input.Cause.ID)},
		},
	}
	results := h.producer.ProduceSync(ctx, record)
	if len(results) != 1 {
		return result, fmt.Errorf("kafka alert hook returned %d produce results, want 1", len(results))
	}
	if results[0].Err != nil {
		return result, fmt.Errorf("publish alert %q to topic %q: %w", input.Alert.AlertID, h.config.Topic, results[0].Err)
	}
	return result, nil
}

// Close 释放 Hook 自己创建的 Kafka client；重复调用安全。
func (h *Hook) Close() {
	h.closeOnce.Do(h.producer.Close)
}

func validateInput(input lifecycle.FinalHookInput) error {
	if err := input.Cause.Validate(); err != nil {
		return fmt.Errorf("kafka alert hook cause: %w", err)
	}
	if err := input.Alert.Validate(); err != nil {
		return fmt.Errorf("kafka alert hook alert: %w", err)
	}
	switch input.Outcome {
	case lifecycle.OutcomeAlertCreated,
		lifecycle.OutcomeAlertUpdated,
		lifecycle.OutcomeAlertRecovered,
		lifecycle.OutcomeAlertClosed:
		return nil
	default:
		return fmt.Errorf("kafka alert hook outcome is invalid: %q", input.Outcome)
	}
}

func messageID(alert domain.Alert) string {
	return digestStrings(
		messageIDDomain,
		alert.BKTenantID,
		alert.AlertID,
		alert.UpdateAt.UTC().Format(time.RFC3339Nano),
	)
}

func partitionKey(alert domain.Alert) string {
	return digestStrings(partitionKeyDomain, alert.BKTenantID, alert.AlertID)
}

func digestStrings(values ...string) string {
	digest := sha256.New()
	for _, value := range values {
		writeLengthPrefixed(digest, value)
	}
	return strings.ToLower(messageIDEncoding.EncodeToString(digest.Sum(nil)))
}

func writeLengthPrefixed(destination hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = destination.Write(length[:])
	_, _ = destination.Write([]byte(value))
}

var _ lifecycle.FinalHook = (*Hook)(nil)
