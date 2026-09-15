// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kachook

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

	"github.com/twmb/franz-go/pkg/kgo"
	"linkd/internal/kafkaclient"
	"linkd/internal/lifecycle"
)

const messageIDDomain = "linkd:kac-alarm-message"

var messageIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

type producer interface {
	ProduceSync(context.Context, ...*kgo.Record) kgo.ProduceResults
	Close()
}

// Hook 将 Alert 快照转换为 KAC Alarm JSON 并同步发送到 Kafka。
type Hook struct {
	config    Config
	name      string
	producer  producer
	closeOnce sync.Once
}

// New 创建拥有独立 Kafka client 的 KAC FinalHook。
func New(config Config, name string) (*Hook, error) {
	config = config.WithDefaults()
	if name == "" {
		return nil, fmt.Errorf("create KAC alarm hook: name is required")
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("create KAC alarm hook: %w", err)
	}
	options, err := kafkaclient.ClientOptions(config.Brokers, config.ClientID, config.Security)
	if err != nil {
		return nil, fmt.Errorf("create KAC alarm hook options: %w", err)
	}
	options = append(options, kgo.RequiredAcks(kgo.AllISRAcks()), kgo.ProducerBatchMaxBytes(int32(config.MaxMessageBytes))) //nolint:gosec // 配置已限制为 MaxInt32。
	client, err := kgo.NewClient(options...)
	if err != nil {
		return nil, fmt.Errorf("create KAC alarm hook client: %w", err)
	}
	return newHook(config, name, client), nil
}

func newHook(config Config, name string, producer producer) *Hook {
	return &Hook{config: config.WithDefaults(), name: name, producer: producer}
}

// Execute 发送单个 KAC Alarm JSON；MessageID 在同一快照重试中保持稳定。
func (h *Hook) Execute(ctx context.Context, input lifecycle.FinalHookInput) (lifecycle.FinalHookResult, error) {
	result := lifecycle.FinalHookResult{
		Name: h.name, Transport: "kafka", Destination: h.config.Topic,
		MessageID: messageID(h.name, input),
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	message, err := convertMessage(input)
	if err != nil {
		return result, err
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return result, fmt.Errorf("marshal KAC alarm message: %w", err)
	}
	if len(payload) > h.config.MaxMessageBytes {
		return result, fmt.Errorf("KAC alarm payload exceeds %d bytes: %d", h.config.MaxMessageBytes, len(payload))
	}
	record := &kgo.Record{
		Topic: h.config.Topic, Key: []byte(message.EventID), Value: payload, Timestamp: input.Alert.UpdateAt,
	}
	results := h.producer.ProduceSync(ctx, record)
	if len(results) != 1 {
		return result, fmt.Errorf("KAC alarm hook returned %d produce results, want 1", len(results))
	}
	if results[0].Err != nil {
		return result, fmt.Errorf("publish KAC alarm %q to topic %q: %w", message.AlarmID, h.config.Topic, results[0].Err)
	}
	return result, nil
}

// Close 释放 Hook 持有的 Kafka client；重复调用安全。
func (h *Hook) Close() {
	h.closeOnce.Do(h.producer.Close)
}

func messageID(name string, input lifecycle.FinalHookInput) string {
	return identityPrefix + digestStrings(
		messageIDDomain, input.Alert.BKTenantID, name, input.Alert.AlertID,
		input.Alert.UpdateAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), string(input.Outcome),
	)
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
