// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kaccompat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"linkd/internal/config"
	"linkd/internal/domain"
)

type fakeKACRabbitChannel struct {
	confirmed chan amqp.Confirmation
	returned  chan amqp.Return
	closed    chan *amqp.Error
	publish   func(context.Context, string, string, bool, bool, amqp.Publishing) error
}

func (f *fakeKACRabbitChannel) NotifyPublish(c chan amqp.Confirmation) chan amqp.Confirmation {
	f.confirmed = c
	return c
}

func (f *fakeKACRabbitChannel) NotifyReturn(c chan amqp.Return) chan amqp.Return {
	f.returned = c
	return c
}

func (f *fakeKACRabbitChannel) NotifyClose(c chan *amqp.Error) chan *amqp.Error {
	f.closed = c
	return c
}

func (f *fakeKACRabbitChannel) PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, message amqp.Publishing) error {
	return f.publish(ctx, exchange, key, mandatory, immediate, message)
}

func rabbitAction(tenant, id string) ActionRequest {
	return ActionRequest{SchemaVersion: "linkd.kac-action.v3", TenantID: tenant, AlertID: "alert-a", AlertAction: domain.AlertAction{ID: id, Type: "firing", Cause: domain.AlertActionCause{Type: "source_event", ID: "event-a"}}}
}

func TestRabbitMQCeleryProtocol(t *testing.T) {
	fake := &fakeKACRabbitChannel{}
	fake.publish = func(_ context.Context, exchange, key string, mandatory, immediate bool, message amqp.Publishing) error {
		if exchange != "" || key != "kac_alarm_execute" || !mandatory || immediate {
			t.Fatal("incorrect queue routing", exchange, key, mandatory, immediate)
		}
		if message.ContentType != "application/json" || message.ContentEncoding != "utf-8" || message.DeliveryMode != amqp.Persistent || message.CorrelationId != "action-a" {
			t.Fatal("incorrect AMQP properties", message)
		}
		for k, want := range map[string]any{"task": kacCeleryTask, "id": "action-a", "root_id": "action-a", "lang": "py", "bk_tenant_id": "tenant-a", "linkd_action_id": "action-a"} {
			if message.Headers[k] != want {
				t.Fatalf("header %s=%v, want %v", k, message.Headers[k], want)
			}
		}
		var body []json.RawMessage
		if err := json.Unmarshal(message.Body, &body); err != nil || len(body) != 3 || string(body[0]) != "[]" {
			t.Fatal("invalid Celery V2 body", string(message.Body), err)
		}
		var kwargs struct {
			Action struct {
				ActionRequest
				AlarmID string `json:"alarm_id"`
			} `json:"action"`
		}
		if err := json.Unmarshal(body[1], &kwargs); err != nil {
			t.Fatal(err)
		}
		if kwargs.Action.AlarmID != "alert-a" || kwargs.Action.TenantID != "tenant-a" || kwargs.Action.ID != "action-a" || kwargs.Action.Cause.ID != "event-a" {
			t.Fatal("action identity or tenant lost", kwargs)
		}
		var embed map[string]any
		if err := json.Unmarshal(body[2], &embed); err != nil || len(embed) != 4 {
			t.Fatal("invalid Celery embed", embed, err)
		}
		for _, key := range []string{"callbacks", "errbacks", "chain", "chord"} {
			if value, ok := embed[key]; !ok || value != nil {
				t.Fatal("unexpected Celery continuation", key)
			}
		}
		fake.confirmed <- amqp.Confirmation{DeliveryTag: 1, Ack: true}
		return nil
	}
	closed := false
	s := NewRabbitMQSender(config.KACRabbitMQConfig{})
	s.open = func(context.Context) (rabbitMQChannel, func(), error) { return fake, func() { closed = true }, nil }
	if err := s.Send(t.Context(), rabbitAction("tenant-a", "action-a")); err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("publisher connection leaked")
	}
}

func TestRabbitMQPublicationFailures(t *testing.T) {
	writeErr := errors.New("write failed")
	for _, name := range []string{"nack", "unroutable_ack", "channel_closed", "write_failed", "deadline"} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeKACRabbitChannel{}
			fake.publish = func(context.Context, string, string, bool, bool, amqp.Publishing) error {
				switch name {
				case "nack":
					fake.confirmed <- amqp.Confirmation{Ack: false}
				case "unroutable_ack":
					fake.returned <- amqp.Return{ReplyCode: 312}
					fake.confirmed <- amqp.Confirmation{Ack: true}
				case "channel_closed":
					fake.closed <- &amqp.Error{Code: 320, Reason: "connection lost"}
				case "write_failed":
					return writeErr
				case "deadline":
				}
				return nil
			}
			var closed int
			s := NewRabbitMQSender(config.KACRabbitMQConfig{})
			s.open = func(context.Context) (rabbitMQChannel, func(), error) { return fake, func() { closed++ }, nil }
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			err := s.Send(ctx, rabbitAction("tenant", "action"))
			if err == nil || closed != 1 {
				t.Fatal("failure accepted or connection leaked", err, closed)
			}
			if name == "write_failed" && !errors.Is(err, writeErr) {
				t.Fatal("underlying publication error lost", err)
			}
			if name == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("deadline lost", err)
			}
		})
	}
	t.Run("dial_failed", func(t *testing.T) {
		s := NewRabbitMQSender(config.KACRabbitMQConfig{})
		s.open = func(context.Context) (rabbitMQChannel, func(), error) { return nil, nil, writeErr }
		if err := s.Send(t.Context(), rabbitAction("tenant", "action")); !errors.Is(err, writeErr) {
			t.Fatal(err)
		}
	})
	t.Run("already_canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		s := NewRabbitMQSender(config.KACRabbitMQConfig{})
		s.open = func(context.Context) (rabbitMQChannel, func(), error) {
			t.Fatal("canceled send opened a connection")
			return nil, nil, nil
		}
		if err := s.Send(ctx, rabbitAction("tenant", "action")); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestRabbitMQConcurrentSendsKeepTenantScope(t *testing.T) {
	var published, cleaned atomic.Int32
	s := NewRabbitMQSender(config.KACRabbitMQConfig{})
	s.open = func(context.Context) (rabbitMQChannel, func(), error) {
		fake := &fakeKACRabbitChannel{}
		fake.publish = func(_ context.Context, _, _ string, _, _ bool, message amqp.Publishing) error {
			var body []json.RawMessage
			if err := json.Unmarshal(message.Body, &body); err != nil {
				t.Error(err)
				return err
			}
			var kwargs struct {
				Action ActionRequest `json:"action"`
			}
			if err := json.Unmarshal(body[1], &kwargs); err != nil {
				t.Error(err)
				return err
			}
			if message.Headers["bk_tenant_id"] != kwargs.Action.TenantID || message.Headers["id"] != kwargs.Action.ID {
				t.Error("concurrent send mixed tenant or identity")
			}
			published.Add(1)
			fake.confirmed <- amqp.Confirmation{Ack: true}
			return nil
		}
		return fake, func() { cleaned.Add(1) }, nil
	}
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			if err := s.Send(t.Context(), rabbitAction(fmt.Sprintf("tenant-%d", i), fmt.Sprintf("action-%d", i))); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if published.Load() != 16 || cleaned.Load() != 16 {
		t.Fatal("concurrent messages or connections lost", published.Load(), cleaned.Load())
	}
}

func TestRabbitMQHandshakeCancellationClosesSocket(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	accepted, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		close(accepted)
		_, _ = io.Copy(io.Discard, conn)
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	s := NewRabbitMQSender(config.KACRabbitMQConfig{URL: "amqp://guest:guest@" + listener.Addr().String() + "/"})
	go func() { done <- s.Send(ctx, rabbitAction("tenant", "action")) }()
	waitSignal(t, accepted)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AMQP handshake ignored cancellation")
	}
	waitSignal(t, exited)
}
