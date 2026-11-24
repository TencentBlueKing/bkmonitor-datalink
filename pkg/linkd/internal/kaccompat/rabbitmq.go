// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kaccompat

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"linkd/internal/config"
)

const kacCeleryTask = "kingeye.kac.alarm.celery_tasks.alarm_process"

type rabbitMQChannel interface {
	NotifyPublish(chan amqp.Confirmation) chan amqp.Confirmation
	NotifyReturn(chan amqp.Return) chan amqp.Return
	NotifyClose(chan *amqp.Error) chan *amqp.Error
	PublishWithContext(context.Context, string, string, bool, bool, amqp.Publishing) error
}

// RabbitMQSender 将 V3 动作编码为 Celery V2 JSON 消息，等待 Broker 确认后返回。
// 每次调用独立连接；并发由动作运行器限制，取消只关闭本次连接，不影响其他动作。
type RabbitMQSender struct {
	config config.KACRabbitMQConfig
	open   func(context.Context) (rabbitMQChannel, func(), error)
}

// NewRabbitMQSender 创建不声明队列、不自动重发消息的 Celery 发布器。
func NewRabbitMQSender(cfg config.KACRabbitMQConfig) *RabbitMQSender {
	return &RabbitMQSender{config: cfg.WithDefaults(), open: func(ctx context.Context) (rabbitMQChannel, func(), error) {
		return openKACRabbitMQ(ctx, cfg.URL)
	}}
}

// Send 成功仅表示 Broker 已接受消息；超时或断连可能已经入队，不能据此自动重投。
// task_id 沿用 action_id 用于关联，不依赖 Celery 按相同 task_id 自动去重。
func (s *RabbitMQSender) Send(ctx context.Context, q ActionRequest) error {
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := call.Err(); err != nil {
		return err
	}
	message, err := celeryActionMessage(q)
	if err != nil {
		return err
	}
	channel, closeConnection, err := s.open(call)
	if err != nil {
		if call.Err() != nil {
			return call.Err()
		}
		return fmt.Errorf("open KAC RabbitMQ publisher: %w", err)
	}
	defer closeConnection()
	confirmed := channel.NotifyPublish(make(chan amqp.Confirmation, 1))
	returned := channel.NotifyReturn(make(chan amqp.Return, 1))
	closed := channel.NotifyClose(make(chan *amqp.Error, 1))
	// 默认 exchange 按队列名直达已有队列；mandatory 防止无路由消息被当作成功。
	if err := channel.PublishWithContext(call, "", s.config.Queue, true, false, message); err != nil {
		if call.Err() != nil {
			return call.Err()
		}
		return fmt.Errorf("publish KAC Celery task: %w", err)
	}
	select {
	case <-call.Done():
		return call.Err()
	case reply, ok := <-returned:
		if !ok {
			return fmt.Errorf("KAC RabbitMQ channel closed before confirmation")
		}
		return fmt.Errorf("KAC RabbitMQ task unroutable (code %d)", reply.ReplyCode)
	case confirmation, ok := <-confirmed:
		if !ok || !confirmation.Ack {
			return fmt.Errorf("KAC RabbitMQ task was not acknowledged")
		}
		// amqp091-go 先投递 basic.return 再投递 confirm；即便 select 先选中 ACK，
		// 也必须检查已缓冲的退回消息，不能把不存在的队列解释为投递成功。
		select {
		case reply, ok := <-returned:
			if ok {
				return fmt.Errorf("KAC RabbitMQ task unroutable (code %d)", reply.ReplyCode)
			}
		default:
		}
		return nil
	case err := <-closed:
		if err != nil {
			return fmt.Errorf("KAC RabbitMQ channel closed: %w", err)
		}
		return fmt.Errorf("KAC RabbitMQ channel closed before confirmation")
	}
}

func celeryActionMessage(q ActionRequest) (amqp.Publishing, error) {
	action := struct {
		ActionRequest
		AlarmID string `json:"alarm_id"`
	}{q, q.AlertID}
	body, err := json.Marshal([3]any{[]any{}, map[string]any{"action": action}, map[string]any{"callbacks": nil, "errbacks": nil, "chain": nil, "chord": nil}})
	if err != nil {
		return amqp.Publishing{}, fmt.Errorf("encode KAC Celery task: %w", err)
	}
	return amqp.Publishing{
		ContentType: "application/json", ContentEncoding: "utf-8", DeliveryMode: amqp.Persistent,
		CorrelationId: q.ID,
		Headers:       amqp.Table{"lang": "py", "task": kacCeleryTask, "id": q.ID, "root_id": q.ID, "retries": int32(0), "bk_tenant_id": q.TenantID, "linkd_action_id": q.ID},
		Body:          body,
	}, nil
}

func openKACRabbitMQ(ctx context.Context, address string) (rabbitMQChannel, func(), error) {
	var socket net.Conn
	var stop func() bool
	connection, err := amqp.DialConfig(address, amqp.Config{Dial: func(network, addr string) (net.Conn, error) {
		var dialErr error
		socket, dialErr = (&net.Dialer{}).DialContext(ctx, network, addr)
		if dialErr != nil {
			return nil, dialErr
		}
		deadline, _ := ctx.Deadline()
		if err := socket.SetDeadline(deadline); err != nil {
			_ = socket.Close()
			return nil, err
		}
		// AMQP 握手、channel RPC 和 Publish 的底层 I/O 不随 context 取消，
		// 使用本次独立 socket 的 deadline 与关闭回调覆盖整个调用生命周期。
		stop = context.AfterFunc(ctx, func() { _ = socket.Close() })
		return socket, nil
	}})
	cleanup := func() {
		if connection != nil {
			deadline, _ := ctx.Deadline()
			_ = connection.CloseDeadline(deadline)
		}
		if stop != nil {
			stop()
		}
		if socket != nil {
			_ = socket.Close()
		}
	}
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	channel, err := connection.Channel()
	if err == nil {
		err = channel.Confirm(false)
	}
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return channel, cleanup, nil
}
