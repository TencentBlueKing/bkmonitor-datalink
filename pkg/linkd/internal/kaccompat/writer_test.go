// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kaccompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/projection"
	"linkd/internal/store/storetest"
)

type fakeRecord struct {
	source map[string]any
	seq    int64
}

type fakeES struct {
	mu              sync.Mutex
	alias, index    string
	indices         map[string]bool
	docs            map[string]fakeRecord
	refreshFailures int
	denied          bool
	before          func(*http.Request)
}

func newFakeES() *fakeES {
	return &fakeES{alias: "kac_alarm_event", index: "kac_alarm_event-000001", indices: map[string]bool{"kac_alarm_event-000001": true}, docs: map[string]fakeRecord{}}
}

func jsonResponse(code int, value any) *http.Response {
	raw, _ := json.Marshal(value)
	return &http.Response{StatusCode: code, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}
}

func (f *fakeES) Perform(req *http.Request) (*http.Response, error) {
	if f.before != nil {
		f.before(req)
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.denied {
		return jsonResponse(403, map[string]any{"error": "private-secret"}), nil
	}
	var body map[string]any
	if req.Body != nil {
		d := json.NewDecoder(req.Body)
		d.UseNumber()
		_ = d.Decode(&body)
	}
	path := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	if req.Method == http.MethodHead {
		return jsonResponse(200, nil), nil
	}
	if path[0] == "_alias" {
		return jsonResponse(200, map[string]any{f.index: map[string]any{"aliases": map[string]any{f.alias: map[string]any{"is_write_index": true}}}}), nil
	}
	if len(path) == 2 && path[1] == "_search" {
		encoded, _ := json.Marshal(body)
		var hits []any
		for key, row := range f.docs {
			index, id, _ := strings.Cut(key, "/")
			if f.indices[index] && strings.Contains(string(encoded), id) && strings.Contains(string(encoded), row.source["bk_tenant_id"].(string)) {
				hits = append(hits, map[string]any{"_index": index, "_id": id})
			}
		}
		return jsonResponse(200, map[string]any{"timed_out": false, "_shards": map[string]any{"failed": 0, "total": 1}, "hits": map[string]any{"hits": hits}}), nil
	}
	if len(path) == 2 && path[1] == "_refresh" {
		if f.refreshFailures > 0 {
			f.refreshFailures--
			return jsonResponse(503, nil), nil
		}
		return jsonResponse(200, map[string]any{"_shards": map[string]any{"failed": 0, "total": 1}}), nil
	}
	if len(path) != 3 {
		return jsonResponse(500, map[string]any{"error": "unexpected path"}), nil
	}
	key := path[0] + "/" + path[2]
	old, exists := f.docs[key]
	if req.Method == http.MethodGet {
		if !exists {
			return jsonResponse(404, map[string]any{"found": false}), nil
		}
		return jsonResponse(200, map[string]any{"found": true, "_seq_no": old.seq, "_primary_term": 1, "_source": old.source}), nil
	}
	if path[1] == "_create" && exists {
		return jsonResponse(409, nil), nil
	}
	if seq := req.URL.Query().Get("if_seq_no"); seq != "" && (!exists || seq != strconv.FormatInt(old.seq, 10)) {
		return jsonResponse(409, nil), nil
	}
	if path[1] == "_update" {
		if !exists {
			return jsonResponse(404, nil), nil
		}
		next := cloneFields(old.source)
		for k, v := range body["doc"].(map[string]any) {
			next[k] = v
		}
		body = next
	}
	f.docs[key] = fakeRecord{source: body, seq: old.seq + 1}
	return jsonResponse(200, map[string]any{"_seq_no": old.seq + 1, "_primary_term": 1, "_id": path[2], "result": "updated"}), nil
}

func (f *fakeES) alarm(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, r := range f.docs {
		index, _, _ := strings.Cut(key, "/")
		if f.indices[index] && r.source["alarm_id"] == id {
			return cloneFields(r.source)
		}
	}
	return nil
}

func (f *fakeES) disposition(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, r := range f.docs {
		if r.source["alarm_id"] == id {
			r.source["status"] = "executing"
			r.source["conductor"] = []any{"operator"}
			r.source["notify_status"] = "sent"
			r.source["field_extra_info"] = map[string]any{"strategy_name": map[string]any{"snapshot_id": "kac-snapshot"}}
			r.seq++
			f.docs[key] = r
		}
	}
}

func fixture(t *testing.T) (*Client, *fakeES, domain.Alert) {
	t.Helper()
	es := newFakeES()
	c := newClient(config.KACPluginConfig{Enabled: true, AlarmEventIndex: es.alias}, es, nil, func(s string) (string, error) { return s, nil })
	c.ready.Store(true)
	a := storetest.Alert("tenant-a", "alert-a", "event-a", "fp", "warning")
	a.Content = "fixture content"
	return c, es, a
}

func project(t *testing.T, c *Client, a domain.Alert) projection.Receipt {
	t.Helper()
	q, e := projection.BuildRequest(a, "kac")
	if e != nil {
		t.Fatal(e)
	}
	r, e := c.Send(t.Context(), projection.Destination{TargetID: "kac"}, q)
	if e != nil || r.ValidateFor(q) != nil {
		t.Fatalf("project revision %d: %v / %+v", a.Revision, e, r)
	}
	return r
}

func advance(a *domain.Alert) { a.Revision++; a.UpdateAt = a.UpdateAt.Add(time.Second) }

func TestWriterProjectsLegacyCleanedFieldsAndFrozenCustomCatalog(t *testing.T) {
	c, es, a := fixture(t)
	a.ExtraData = domain.JSONObject{
		"object": json.RawMessage(`"legacy object without identity"`), "item": json.RawMessage(`false`),
		"meta_info": json.RawMessage(`""`), "strategy_id": json.RawMessage(`"third-party-strategy"`),
		"dimension_info": json.RawMessage(`0`), "owner": json.RawMessage(`"old"`),
		"details":         json.RawMessage(`{"count":9007199254740993,"enabled":false,"items":["a",0]}`),
		"private_payload": json.RawMessage(`"do not expand"`), "status": json.RawMessage(`"executing"`),
		"field_extra_info": json.RawMessage(`{"item":{"url":"/metric"},"strategy_name":{"url":"/legacy"}}`),
	}
	a.Enrich = domain.JSONObject{"processors": json.RawMessage(`[
		{"metric":{"status":"succeeded","patches":[{"op":"set","path":"$.labels.display_name","value":"default metric"}]}},
		{"fields":{"status":"succeeded","patches":[
			{"op":"set","path":"$.extra_data.owner","value":"alice"},
			{"op":"set","path":"$.extra_data.__kac_custom_fields","value":["owner","details","missing"]}
		]}}
	]`)}
	first := project(t, c, a)
	row := es.alarm(first.AlarmID)
	for field, want := range map[string]any{"object": "legacy object without identity", "item": "false", "meta_info": "", "strategy_id": "third-party-strategy", "dimension_info": "0", "owner": "alice", "status": "received", "bk_tenant_id": "tenant-a"} {
		if row[field] != want {
			t.Fatalf("%s=%+v want=%+v", field, row[field], want)
		}
	}
	for _, field := range []string{"private_payload", "missing", "__kac_custom_fields"} {
		if _, found := row[field]; found {
			t.Fatalf("expanded unregistered or missing field %s", field)
		}
	}
	details := row["details"].(map[string]any)
	if details["count"] != json.Number("9007199254740993") || details["enabled"] != false {
		t.Fatal("custom JSON types or precision lost", details)
	}
	extra := row["field_extra_info"].(map[string]any)
	if extra["item"].(map[string]any)["url"] != "/metric" {
		t.Fatal("cleaned metric link lost", extra)
	}
	es.disposition(first.AlarmID)
	advance(&a)
	project(t, c, a)
	extra = es.alarm(first.AlarmID)["field_extra_info"].(map[string]any)
	if extra["strategy_name"].(map[string]any)["snapshot_id"] != "kac-snapshot" {
		t.Fatal("KAC snapshot link lost on legacy projection", extra)
	}
}

func TestWriterRejectsProtectedAndInvalidCustomCatalog(t *testing.T) {
	for _, field := range []string{"alarm_id", "event_id", "bk_tenant_id", "status", "conductor", "notify_status", "associate_count", "strategy_config_uid", "__kac_custom_fields"} {
		t.Run(field, func(t *testing.T) {
			c, es, a := fixture(t)
			raw, _ := json.Marshal([]string{field})
			a.ExtraData["__kac_custom_fields"] = raw
			q, err := projection.BuildRequest(a, "kac")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Send(t.Context(), projection.Destination{TargetID: "kac"}, q); err == nil || len(es.docs) != 0 {
				t.Fatal("protected custom field reached storage", err)
			}
		})
	}
}

func TestWriterRolloverAndKACDisposalFields(t *testing.T) {
	c, es, a := fixture(t)
	first := project(t, c, a)
	es.disposition(first.AlarmID)
	es.mu.Lock()
	es.index = "kac_alarm_event-000002"
	es.indices[es.index] = true
	es.mu.Unlock()
	advance(&a)
	a.Severity = "critical"
	next := project(t, c, a)
	row := es.alarm(first.AlarmID)
	if first.DocumentRef != next.DocumentRef || row["status"] != "executing" || row["notify_status"] != "sent" || row["level"] != "critical" {
		t.Fatal("rollover or disposal lost", row)
	}
	extra := row["field_extra_info"].(map[string]any)["strategy_name"].(map[string]any)
	if extra["snapshot_id"] != "kac-snapshot" {
		t.Fatal("nested KAC field overwritten")
	}
	// 旧快照不能退回新级别，重复快照不能重置处置态。
	a.Revision = 1
	a.Severity = "warning"
	a.UpdateAt = a.UpdateAt.Add(-time.Second)
	project(t, c, a)
	if es.alarm(first.AlarmID)["level"] != "critical" {
		t.Fatal("stale projection regressed")
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	count := 0
	for key := range es.docs {
		if strings.HasPrefix(key, "kac_alarm_event-") {
			count++
		}
	}
	if count != 1 {
		t.Fatal("rollover duplicated document", count)
	}
}

func TestWriterRestoresDisposalAfterMergeWaitAndEndsLifecycle(t *testing.T) {
	c, es, a := fixture(t)
	r := project(t, c, a)
	es.disposition(r.AlarmID)
	advance(&a)
	// 原始告警等待合并时使用 pending_merge，不改变 active 生命周期。
	a.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{{WindowID: strings.Repeat("a", 64), Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("b", 64)}, GroupKey: strings.Repeat("c", 64), MemberEventID: "event-a", Severity: a.Severity, Groups: []int{0}, StartedAt: a.UpdateAt, Deadline: a.UpdateAt.Add(time.Minute)}}}
	project(t, c, a)
	if es.alarm(r.AlarmID)["status"] != "pending_merge" {
		t.Fatal("merge state missing")
	}
	advance(&a)
	a.Merge = &domain.AlertMerge{Role: "original", State: "released"}
	project(t, c, a)
	if es.alarm(r.AlarmID)["status"] != "executing" {
		t.Fatal("disposal not restored")
	}
	advance(&a)
	end := a.UpdateAt
	a.EndAt = &end
	a.Status = domain.AlertStatusRecovered
	a.EndType = domain.AlertEndTypeSource
	a.EndReason = "resolved"
	project(t, c, a)
	row := es.alarm(r.AlarmID)
	if row["status"] != "restored" || row["source_alarm_status"] != "resolved" || row["notify_status"] != "sent" {
		t.Fatal("terminal mapping", row)
	}
}

func TestWriterVisibilityFailureAndIdentityConflicts(t *testing.T) {
	c, es, a := fixture(t)
	q, e := projection.BuildRequest(a, "kac")
	if e != nil {
		t.Fatal(e)
	}
	es.refreshFailures = 1
	if _, e = c.Send(t.Context(), projection.Destination{TargetID: "kac"}, q); e == nil {
		t.Fatal("refresh failure acknowledged")
	}
	project(t, c, a)
	changed := a.Clone()
	changed.Title = "same revision different facts"
	bad, e := projection.BuildRequest(changed, "kac")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Send(t.Context(), projection.Destination{TargetID: "kac"}, bad); e == nil {
		t.Fatal("conflicting version accepted")
	}
	es.denied = true
	_, e = c.Send(t.Context(), projection.Destination{TargetID: "kac"}, q)
	if e == nil || strings.Contains(e.Error(), "private") {
		t.Fatal("auth failure leaked or acknowledged", e)
	}
}

func TestWriterConcurrentTenantsAndCancellation(t *testing.T) {
	c, es, a := fixture(t)
	var wg sync.WaitGroup
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		wg.Go(func() { v := a.Clone(); v.BKTenantID = tenant; project(t, c, v) })
	}
	wg.Wait()
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		id, _ := projection.AlarmID(tenant, a.AlertID)
		if es.alarm(id)["bk_tenant_id"] != tenant {
			t.Fatal("tenant collision")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	q, _ := projection.BuildRequest(a, "kac")
	if _, err := c.Send(ctx, projection.Destination{TargetID: "kac"}, q); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestClientBoundsParallelRequestsAndExitsOnCancel(t *testing.T) {
	c, es, _ := fixture(t)
	var in, max atomic.Int64
	es.before = func(r *http.Request) {
		n := in.Add(1)
		for old := max.Load(); n > old && !max.CompareAndSwap(old, n); old = max.Load() {
		}
		<-r.Context().Done()
		in.Add(-1)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() { _ = c.request(ctx, http.MethodGet, "/_alias/kac_alarm_event", nil, nil, nil) })
	}
	deadline := time.After(time.Second)
	for max.Load() < 4 {
		select {
		case <-deadline:
			t.Fatal("requests did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	wg.Wait()
	if max.Load() != 4 || in.Load() != 0 {
		t.Fatal("request bound or exit", max.Load(), in.Load())
	}
}

func TestDelayedOldWriteCannotOverwriteNewerProjection(t *testing.T) {
	c, es, a := fixture(t)
	receipt := project(t, c, a)
	paused := make(chan struct{})
	resume := make(chan struct{})
	var held atomic.Bool
	es.before = func(r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/_update/") && held.CompareAndSwap(false, true) {
			close(paused)
			select {
			case <-resume:
			case <-r.Context().Done():
			}
		}
	}
	old := a.Clone()
	advance(&old)
	old.Title = "older in-flight update"
	q, err := projection.BuildRequest(old, "kac")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := c.Send(t.Context(), projection.Destination{TargetID: "kac"}, q); done <- e }()
	select {
	case <-paused:
	case <-time.After(time.Second):
		t.Fatal("old write did not start")
	}
	next := old.Clone()
	advance(&next)
	next.Title = "newest update"
	project(t, c, next)
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if es.alarm(receipt.AlarmID)["name"] != "newest update" {
		t.Fatal("delayed writer regressed current document")
	}
}

func TestShieldTransitionsPreserveLiveKACDisposition(t *testing.T) {
	c, es, a := fixture(t)
	receipt := project(t, c, a)
	es.disposition(receipt.AlarmID)
	advance(&a)
	check := a.UpdateAt
	a.Shield = domain.AlertShield{Active: true, NextCheckAt: &check, Bindings: []domain.ShieldBinding{{BindingID: strings.Repeat("a", 64), Policy: domain.PolicyVersion{ID: "shield", Version: 1, Digest: strings.Repeat("b", 64)}, Type: "time_shield", ActivationID: strings.Repeat("c", 64), SourceEventID: "event-a", Severity: a.Severity, BoundAt: a.UpdateAt}}}
	project(t, c, a)
	if es.alarm(receipt.AlarmID)["status"] != "shielded" {
		t.Fatal("shield transition missing")
	}
	// 已发生的处置可以继续更新；没有策略状态转换的普通同步保留其处置状态。
	es.disposition(receipt.AlarmID)
	advance(&a)
	project(t, c, a)
	if es.alarm(receipt.AlarmID)["status"] != "executing" {
		t.Fatal("ordinary sync overwrote KAC disposition")
	}
	advance(&a)
	a.Shield = domain.AlertShield{}
	project(t, c, a)
	if es.alarm(receipt.AlarmID)["status"] != "executing" {
		t.Fatal("unshield lost saved disposition")
	}
}

func TestNewSnapshotFinishesPartiallyAppliedPolicyTransition(t *testing.T) {
	c, es, a := fixture(t)
	receipt := project(t, c, a)
	es.disposition(receipt.AlarmID)
	advance(&a)
	check := a.UpdateAt
	a.Shield = domain.AlertShield{Active: true, NextCheckAt: &check, Bindings: []domain.ShieldBinding{{BindingID: strings.Repeat("a", 64), ActivationID: strings.Repeat("c", 64), Policy: domain.PolicyVersion{ID: "shield", Version: 1, Digest: strings.Repeat("b", 64)}, Type: "time_shield", SourceEventID: "event-a", Severity: a.Severity, BoundAt: a.UpdateAt}}}
	q, err := projection.BuildRequest(a, "kac")
	if err != nil {
		t.Fatal(err)
	}
	es.refreshFailures = 1
	if _, err = c.Send(t.Context(), projection.Destination{TargetID: "kac"}, q); err == nil {
		t.Fatal("partial write did not fail")
	}
	if es.alarm(receipt.AlarmID)["status"] != "shielded" {
		t.Fatal("fixture did not apply policy state")
	}
	// 不重试旧屏蔽版本，而是直接同步已解除屏蔽的最新完整快照。
	advance(&a)
	a.Shield = domain.AlertShield{}
	project(t, c, a)
	if es.alarm(receipt.AlarmID)["status"] != "executing" {
		t.Fatal("unconfirmed intermediate policy state leaked into newest projection")
	}
}

func TestUnreadyIndexDefersWithoutIssuingDocumentWrites(t *testing.T) {
	c, es, a := fixture(t)
	c.ready.Store(false)
	var calls atomic.Int64
	es.before = func(*http.Request) { calls.Add(1) }
	q, err := projection.BuildRequest(a, "kac")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Send(t.Context(), projection.Destination{TargetID: "kac"}, q)
	var failure projection.Failure
	if !errors.As(err, &failure) || !failure.Retryable || failure.Code != "target_unavailable" || calls.Load() != 0 {
		t.Fatal("unready index was not deferred", err)
	}
}
