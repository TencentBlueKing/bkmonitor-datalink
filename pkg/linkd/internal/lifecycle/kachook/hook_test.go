// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kachook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"linkd/internal/domain"
	"linkd/internal/enrich/kingeye"
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
	var message kingeye.AlarmMessage
	if err := json.Unmarshal(record.Value, &message); err != nil {
		t.Fatal(err)
	}
	if message.AlarmID == message.EventID || message.EventID != "linkd-alert-1" {
		t.Fatalf("message=%+v", message)
	}
	if message.AlarmID != kacAlarmID(input) {
		t.Fatalf("alarm_id=%q want=%q", message.AlarmID, kacAlarmID(input))
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

func TestLegacyKACSkipsAlertsOwnedByReliableActionTargets(t *testing.T) {
	for _, status := range []domain.AlertStatus{domain.AlertStatusActive, domain.AlertStatusRecovered, domain.AlertStatusClosed} {
		for _, reliable := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reliable=%t", status, reliable), func(t *testing.T) {
				a := testAlert()
				a.Status = status
				outcome := lifecycle.OutcomeAlertCreated
				if status.Terminal() {
					a.EndAt = &a.UpdateAt
					a.EndType, a.EndReason = domain.AlertEndTypeSource, "source ended"
					outcome = lifecycle.OutcomeAlertRecovered
					if status == domain.AlertStatusClosed {
						outcome = lifecycle.OutcomeAlertClosed
					}
				}
				a.Projection.Targets = map[string]domain.ProjectionTargetState{"state-only": {SourceVersion: 1, RequiredRevision: a.Revision}, "kac": {SourceVersion: 1, RequiredRevision: a.Revision, ActionEnabled: reliable}}
				producer := &fakeProducer{}
				hook := newHook(Config{Brokers: []string{"kafka:9092"}, Topic: "legacy", MaxMessageBytes: 1 << 20}, "legacy", producer)
				levelCalls := 0
				hook.UseLevelResolver(func(value string) (string, error) { levelCalls++; return kacLevel(value) })
				input := lifecycle.FinalHookInput{Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event"}, Alert: a, Outcome: outcome}
				result, err := hook.Execute(t.Context(), input)
				if err != nil || result.Skipped != reliable || result.Name != "legacy" || result.Transport != "kafka" {
					t.Fatal("wrong legacy ownership result", result, err)
				}
				if reliable {
					if len(producer.records) != 0 || levelCalls != 0 {
						t.Fatal("reliable action was converted/published through legacy KAC")
					}
				} else if len(producer.records) != 1 || levelCalls != 1 {
					t.Fatal("state-only projection disabled legacy actions")
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if _, err := hook.Execute(ctx, input); !errors.Is(err, context.Canceled) {
					t.Fatal("cancel swallowed", err)
				}
				invalidCause := input
				invalidCause.Cause = lifecycle.AlertChangeCause{}
				if result, err := hook.Execute(t.Context(), invalidCause); err == nil || result.Skipped {
					t.Fatal("invalid cause silently skipped", err)
				}
				input.Alert.Projection.Targets["kac"] = domain.ProjectionTargetState{ActionEnabled: true}
				if result, err := hook.Execute(t.Context(), input); err == nil || result.Skipped {
					t.Fatal("invalid binding silently skipped", err)
				}
			})
		}
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

func TestLegacyKACInputOnlyAcceptsAdmittedMergeRelease(t *testing.T) {
	a := testAlert()
	at := a.UpdateAt
	wait := domain.MergeWait{WindowID: strings.Repeat("a", 64), Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("b", 64)}, GroupKey: strings.Repeat("c", 64), MemberEventID: a.TriggerEventID, Severity: a.Severity, Groups: []int{0}, StartedAt: at, Deadline: at.Add(time.Minute)}
	before := &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{wait}}
	a.Merge, _ = before.ReleaseWindow(wait.WindowID)
	a.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: a.Severity, CauseType: lifecycle.AlertChangeCauseSystemOperation, CauseID: "release-op"}
	a.MergeChange = &domain.AlertMergeChange{Kind: "release", OperationID: "release-op", WindowID: wait.WindowID, EffectiveAt: at, Before: before, After: a.Merge.Clone(), ActionReady: true}
	input := lifecycle.FinalHookInput{Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSystemOperation, ID: "release-op"}, Alert: a, Outcome: lifecycle.OutcomeAlertMergeReleased}
	producer := &fakeProducer{}
	hook := newHook(Config{Brokers: []string{"localhost:9092"}, Topic: "alerts", MaxMessageBytes: 1 << 20}, "kac", producer)
	first, err := hook.Execute(t.Context(), input)
	if err != nil || len(producer.records) != 1 {
		t.Fatal("admitted release not published", err)
	}
	var message kingeye.AlarmMessage
	if err := json.Unmarshal(producer.records[0].Value, &message); err != nil || message.Action != "firing" {
		t.Fatal("wrong KAC action", err)
	}
	again, err := hook.Execute(t.Context(), input)
	if err != nil || again.MessageID != first.MessageID {
		t.Fatal("release identity changed", err)
	}
	input.Alert.MergeChange.ActionReady = false
	if _, err := hook.Execute(t.Context(), input); err == nil || len(producer.records) != 2 {
		t.Fatal("pure state release became legacy firing")
	}
	input.Outcome = lifecycle.OutcomeAlertShieldChanged
	input.Alert.MergeChange = nil
	if _, err := hook.Execute(t.Context(), input); err == nil || len(producer.records) != 2 {
		t.Fatal("shield state change became legacy firing")
	}
}
