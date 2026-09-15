// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kachook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
	"linkd/internal/lifecycle"
)

func TestHookEmitsKACAlarmRecord(t *testing.T) {
	producer := &fakeProducer{}
	hook := newHook(Config{Brokers: []string{"localhost:9092"}, Topic: "kac-alerts", MaxMessageBytes: 1 << 20}, "kac-main", producer)
	input := lifecycle.FinalHookInput{
		Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event-1"},
		Alert: testAlert(), Outcome: lifecycle.OutcomeAlertCreated,
	}
	result, err := hook.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Transport != "kafka" || result.Destination != "kac-alerts" || result.MessageID == "" || len(producer.records) != 1 {
		t.Fatalf("result=%+v records=%d", result, len(producer.records))
	}
	record := producer.records[0]
	if string(record.Key) != "linkd-alert-1" || !record.Timestamp.Equal(input.Alert.UpdateAt) || len(record.Headers) != 0 {
		t.Fatalf("record key=%q timestamp=%s headers=%v", record.Key, record.Timestamp, record.Headers)
	}
	var message Message
	if err := json.Unmarshal(record.Value, &message); err != nil {
		t.Fatal(err)
	}
	if message.AlarmID != "linkd-alert-1" || message.EventID != "linkd-alert-1" {
		t.Fatalf("message=%+v", message)
	}
	again, err := hook.Execute(context.Background(), input)
	if err != nil || again.MessageID != result.MessageID {
		t.Fatalf("stable result=%+v err=%v", again, err)
	}
}

func TestHookReportsProducerAndPayloadErrors(t *testing.T) {
	producer := &fakeProducer{err: errors.New("broker failed")}
	hook := newHook(Config{Brokers: []string{"localhost:9092"}, Topic: "kac-alerts", MaxMessageBytes: 1 << 20}, "kac-main", producer)
	input := lifecycle.FinalHookInput{Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event-1"}, Alert: testAlert(), Outcome: lifecycle.OutcomeAlertCreated}
	result, err := hook.Execute(context.Background(), input)
	if err == nil || result.MessageID == "" {
		t.Fatalf("producer result=%+v err=%v", result, err)
	}

	hook = newHook(Config{Brokers: []string{"localhost:9092"}, Topic: "kac-alerts", MaxMessageBytes: 32}, "kac-main", &fakeProducer{})
	if _, err := hook.Execute(context.Background(), input); err == nil {
		t.Fatal("oversized payload accepted")
	}
}

func TestHookCloseIsIdempotent(t *testing.T) {
	producer := &fakeProducer{}
	hook := newHook(Config{Brokers: []string{"localhost:9092"}, Topic: "kac-alerts", MaxMessageBytes: 1 << 20}, "kac-main", producer)
	hook.Close()
	hook.Close()
	if producer.closeCount != 1 {
		t.Fatalf("close count=%d", producer.closeCount)
	}
}

type fakeProducer struct {
	records    []*kgo.Record
	err        error
	closeCount int
}

func (p *fakeProducer) ProduceSync(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
	p.records = append(p.records, records...)
	results := make(kgo.ProduceResults, len(records))
	for index, record := range records {
		results[index] = kgo.ProduceResult{Record: record, Err: p.err}
	}
	return results
}

func (p *fakeProducer) Close() { p.closeCount++ }
