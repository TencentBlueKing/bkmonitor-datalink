// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kaccompat

import (
	"encoding/json"
	"os"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"linkd/internal/config"
)

// TestKACCeleryRabbitMQIntegration 仅在显式提供 Broker 时运行；队列由本次连接独占，
// 不读取或发布 KAC 真实业务队列，连接结束后由 RabbitMQ 清理测试资源。
func TestKACCeleryRabbitMQIntegration(t *testing.T) {
	address := os.Getenv("LINKD_TEST_KAC_AMQP_URL")
	if address == "" {
		t.Skip("set LINKD_TEST_KAC_AMQP_URL for isolated RabbitMQ integration")
	}
	conn, err := amqp.Dial(address)
	if err != nil {
		t.Fatal("connect integration broker", err)
	}
	defer func() { _ = conn.Close() }()
	channel, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	queue, err := channel.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	sender := NewRabbitMQSender(config.KACRabbitMQConfig{URL: address, Queue: queue.Name})
	request := rabbitAction("tenant-integration", "00000000-0000-5000-8000-000000000001")
	if err := sender.Send(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	message, found, err := channel.Get(queue.Name, true)
	if err != nil || !found {
		t.Fatal("confirmed task missing from queue", err)
	}
	var body []json.RawMessage
	if err := json.Unmarshal(message.Body, &body); err != nil || len(body) != 3 {
		t.Fatal("invalid Celery wire body", err)
	}
	if message.Headers["task"] != kacCeleryTask || message.Headers["bk_tenant_id"] != request.TenantID || message.CorrelationId != request.ID || message.DeliveryMode != amqp.Persistent {
		t.Fatal("wire task properties differ", message.Headers)
	}
	sender = NewRabbitMQSender(config.KACRabbitMQConfig{URL: address, Queue: queue.Name + "-missing"})
	if err := sender.Send(t.Context(), request); err == nil {
		t.Fatal("unroutable task was reported as queued")
	}
}
