// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kaccompat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle/kachook"
	"linkd/internal/store/storetest"
)

func integrationClient(t *testing.T) *Client {
	t.Helper()
	endpoint := os.Getenv("LINKD_TEST_ELASTICSEARCH_URL")
	if endpoint == "" {
		t.Skip("set LINKD_TEST_ELASTICSEARCH_URL for real ES integration")
	}
	replicas := 0
	cfg := config.KACPluginConfig{Enabled: true, AlarmEventIndex: "linkd-kac-v3-" + strings.ReplaceAll(uuid.NewString(), "-", ""), Elasticsearch: config.KACElasticsearchConfig{Addresses: []string{endpoint}, NumberOfShards: 1, NumberOfReplicas: &replicas}}
	client, err := Open(cfg, func(s string) (string, error) { return s, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.request(ctx, http.MethodDelete, "/"+client.config.AlarmEventIndex+"*", nil, nil, nil)
		_ = client.request(ctx, http.MethodDelete, "/_template/"+client.config.AlarmEventIndex, nil, nil, nil)
		_ = client.request(ctx, http.MethodDelete, "/_ilm/policy/"+client.config.AlarmEventIndex+"_policy", nil, nil, nil)
		client.Close()
	})
	if err = client.Maintain(t.Context()); err != nil {
		t.Fatal("maintain", err)
	}
	return client
}

func readAlarm(t *testing.T, c *Client, index string, a domain.Alert) map[string]any {
	t.Helper()
	id, _ := kachook.AlarmID(a.BKTenantID, a.AlertID)
	var response struct {
		Source map[string]any `json:"_source"`
	}
	if err := c.request(t.Context(), http.MethodGet, "/"+index+"/_doc/"+id, nil, nil, &response); err != nil {
		t.Fatal(err)
	}
	return response.Source
}

func TestElasticsearchBulkLifecycleAndDisposition(t *testing.T) {
	client := integrationClient(t)
	alert := storetest.Alert("tenant-a", "alert-a", "event-a", "fp-a", "warning")
	result := client.WriteBatch(t.Context(), []domain.Alert{alert}, true)
	if result[0].Err != nil {
		t.Fatal("create", result[0].Err)
	}
	index := result[0].Index
	id, _ := kachook.AlarmID(alert.BKTenantID, alert.AlertID)
	if err := client.request(t.Context(), http.MethodPost, "/"+index+"/_update/"+id, nil, map[string]any{"doc": map[string]any{"status": "executing", "conductor": []string{"operator"}, "tag_info": []string{"kac-tag"}, "field_extra_info": map[string]any{"snapshot": "kac-snapshot"}}}, nil); err != nil {
		t.Fatal(err)
	}
	alert.Severity = "critical"
	alert.PolicyTags = []int64{7}
	for _, blocked := range []bool{true, false} {
		alert.Shield = domain.AlertShield{Active: blocked}
		if out := client.WriteBatch(t.Context(), []domain.Alert{alert}, true); out[0].Err != nil {
			t.Fatal(out[0].Err)
		}
		doc := readAlarm(t, client, index, alert)
		want := "executing"
		if blocked {
			want = "shielded"
		}
		if doc["status"] != want || doc["level"] != "critical" {
			t.Fatal("state mapping", doc["status"], doc["level"])
		}
		if len(doc["tag_info"].([]any)) != 2 || doc["conductor"].([]any)[0] != "operator" || doc["field_extra_info"].(map[string]any)["snapshot"] != "kac-snapshot" {
			t.Fatal("KAC fields overwritten")
		}
	}
	client.relations = relationFunc(func(_ context.Context, tenant, relation string) (domain.MergeRelation, error) {
		return domain.MergeRelation{TenantID: tenant, ID: relation, ParentAlertID: "parent", Members: []domain.MergeRelationMember{{AlertID: alert.AlertID}}}, nil
	})
	alert.Merge = &domain.AlertMerge{Role: "original", State: "merged", RelationIDs: []string{"relation"}}
	if out := client.WriteBatch(t.Context(), []domain.Alert{alert}, true); out[0].Err != nil {
		t.Fatal(out[0].Err)
	}
	merged := readAlarm(t, client, index, alert)
	parentID, _ := kachook.AlarmID(alert.BKTenantID, "parent")
	if merged["status"] != "merged_into" || merged["associate_alarm_id"].([]any)[0] != parentID {
		t.Fatal("merge projection", merged["status"])
	}
	alert.Merge = &domain.AlertMerge{Role: "original", State: "released"}
	if out := client.WriteBatch(t.Context(), []domain.Alert{alert}, true); out[0].Err != nil {
		t.Fatal(out[0].Err)
	}
	released := readAlarm(t, client, index, alert)
	if released["status"] != "executing" || len(released["associate_alarm_id"].([]any)) != 0 {
		t.Fatal("release did not restore disposition")
	}
	// 滚动后客户端重新定位旧告警，更新原物理索引，不在新索引创建重复文档。
	var rollover any
	if err := client.request(t.Context(), http.MethodPost, "/"+client.config.AlarmEventIndex+"/_rollover", nil, map[string]any{}, &rollover); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	clear(client.locations)
	client.mu.Unlock()
	at := alert.UpdateAt.Add(time.Second)
	alert.Status = domain.AlertStatusClosed
	alert.EndAt = &at
	alert.EndType = domain.AlertEndTypeUser
	alert.EndReason = "confirmed"
	if out := client.WriteBatch(t.Context(), []domain.Alert{alert}, true); out[0].Err != nil || out[0].Index != index {
		t.Fatal("rollover close", out)
	}
	doc := readAlarm(t, client, index, alert)
	if doc["status"] != "closed" || doc["close_reason"] != "confirmed" {
		t.Fatal("close not written", doc["status"])
	}
	var response struct {
		Count int `json:"count"`
	}
	if err := client.request(t.Context(), http.MethodPost, "/"+client.config.AlarmEventIndex+"/_count", nil, map[string]any{"query": map[string]any{"ids": map[string]any{"values": []string{id}}}}, &response); err != nil || response.Count != 1 {
		t.Fatal("duplicate lifecycle document", response.Count, err)
	}
	other := alert.Clone()
	other.BKTenantID = "tenant-b"
	if out := client.WriteBatch(t.Context(), []domain.Alert{other}, true); out[0].Err != nil {
		t.Fatal(out[0].Err)
	}
	recovered := storetest.Alert("tenant-a", "recovered-alert", "recovered-opening", "recovered-fp", "warning")
	created := client.WriteBatch(t.Context(), []domain.Alert{recovered}, true)
	if created[0].Err != nil {
		t.Fatal(created[0].Err)
	}
	recovered.Status, recovered.EndAt, recovered.EndType = domain.AlertStatusRecovered, &at, domain.AlertEndTypeSource
	if out := client.WriteBatch(t.Context(), []domain.Alert{recovered}, true); out[0].Err != nil {
		t.Fatal(out[0].Err)
	}
	if got := readAlarm(t, client, created[0].Index, recovered); got["status"] != "restored" || got["action"] != "resolved" {
		t.Fatal("recovery mapping")
	}

}

func TestElasticsearchBulkPartialFailureAndByteSplit(t *testing.T) {
	client := integrationClient(t)
	client.config.Delivery.MaxBatchBytes = 5000
	alerts := []domain.Alert{}
	for i := range 4 {
		a := storetest.Alert("tenant", fmt.Sprintf("alert-%d", i), "event", fmt.Sprint(i), "warning")
		a.Content = strings.Repeat("x", 1100)
		alerts = append(alerts, a)
	}
	for _, result := range client.WriteBatch(t.Context(), alerts, true) {
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	}
	// 一个物理位置失效不能吞掉同批其他项目的成功。
	badID, _ := kachook.AlarmID(alerts[0].BKTenantID, alerts[0].AlertID)
	client.mu.Lock()
	client.locations[badID] = client.config.AlarmEventIndex + "-absent"
	client.mu.Unlock()
	results := client.WriteBatch(t.Context(), alerts, true)
	if results[0].Err == nil || results[1].Err != nil {
		t.Fatal("partial result lost", results)
	}
}

func TestActionHTTPUsesV3WithoutSnapshot(t *testing.T) {
	received := make(chan ActionRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Internal-Token"), "Bearer ") {
			t.Error("JWT missing")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["alert"] != nil || body["content_hash"] != nil || body["linkd_revision"] != nil {
			t.Error("snapshot fields sent")
		}
		raw, _ := json.Marshal(body)
		var request ActionRequest
		_ = json.Unmarshal(raw, &request)
		received <- request
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"outcome":"queued","task_id":"task-1"}`))
	}))
	defer server.Close()
	sender := NewHTTPSender(config.KACPluginConfig{ActionEndpoint: server.URL, JWT: config.JWTConfig{SecretKey: "test-secret", Username: "admin"}})
	defer sender.Close()
	request := ActionRequest{SchemaVersion: "linkd.kac-action.v3", TenantID: "tenant-a", AlertID: "alert-a", AlertAction: domain.AlertAction{ID: "action-a", Type: "firing", Cause: domain.AlertActionCause{Type: "source_event", ID: "event-a"}}}
	if err := sender.Send(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if actual := <-received; actual.SchemaVersion != request.SchemaVersion || actual.TenantID != request.TenantID {
		t.Fatal("wrong request", actual)
	}
}

// TestKACLiveAction 由 Kingeye 隔离联调启动真实接收端与 Celery 后调用。
func TestKACLiveAction(t *testing.T) {
	endpoint := os.Getenv("LINKD_TEST_KAC_LIVE_ENDPOINT")
	if endpoint == "" {
		t.Skip("Kingeye live integration is not configured")
	}
	cfg := config.KACPluginConfig{Enabled: true, AlarmEventIndex: os.Getenv("LINKD_TEST_KAC_LIVE_INDEX"), Elasticsearch: config.KACElasticsearchConfig{Addresses: []string{os.Getenv("LINKD_TEST_ELASTICSEARCH_URL")}}, ActionEndpoint: endpoint, JWT: config.JWTConfig{SecretKey: os.Getenv("LINKD_TEST_KAC_LIVE_JWT"), Username: "admin"}}
	replicas := 0
	cfg.Elasticsearch.NumberOfReplicas = &replicas
	client, err := Open(cfg, func(level string) (string, error) { return level, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err = client.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	alert := storetest.Alert(os.Getenv("LINKD_TEST_KAC_LIVE_TENANT"), os.Getenv("LINKD_TEST_KAC_LIVE_ALERT"), "event", "fingerprint", "warning")
	at := alert.UpdateAt.Add(time.Second)
	alert.Status = domain.AlertStatusClosed
	alert.EndAt = &at
	alert.EndType = domain.AlertEndTypeUser
	alert.EndReason = "真实链路关闭"
	alert.EndOperation = &domain.AlertEndOperation{ID: os.Getenv("LINKD_TEST_KAC_LIVE_OPERATION"), Source: "manual", OperatorKind: domain.OperatorKindUser, OperatorID: "live-operator"}
	if result := client.WriteBatch(t.Context(), []domain.Alert{alert}, true); result[0].Err != nil {
		t.Fatal(result[0].Err)
	}
	change := domain.NewAlertChange(alert, domain.AlertActionCause{Type: "user_operation", ID: alert.EndOperation.ID}, true)
	sender := NewHTTPSender(cfg)
	defer sender.Close()
	if err = sender.Send(t.Context(), ActionRequest{SchemaVersion: "linkd.kac-action.v3", TenantID: alert.BKTenantID, AlertID: alert.AlertID, AlertAction: *change.Action}); err != nil {
		t.Fatal(err)
	}
}

type relationFunc func(context.Context, string, string) (domain.MergeRelation, error)

func (f relationFunc) GetMergeRelation(ctx context.Context, tenant, id string) (domain.MergeRelation, error) {
	return f(ctx, tenant, id)
}
