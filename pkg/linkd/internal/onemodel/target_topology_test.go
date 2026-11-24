// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package onemodel

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const targetNodeResponse = `{"hits":{"hits":[{"_source":{"unique_id":"module-1","bk_tenant_id":"t","bk_biz_id":2,"model_id":"cw-Module","model_inst_id":"1","entity_uid":"cw-Module|1"}}]}}`

func topologyResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
}

func membershipFixture(path, id string) map[string]any {
	return map[string]any{"unique_id": path, "bk_tenant_id": "t", "bk_biz_id": 2, "model_id": "cw-Host", "model_inst_id": id, "entity_uid": "cw-Host|" + id, "topology_ancestor_unique_ids": []string{"biz-2", "module-1"}}
}

func scrollFixture(t *testing.T, rows []map[string]any) string {
	t.Helper()
	hits := []any{}
	for _, row := range rows {
		hits = append(hits, map[string]any{"_source": row})
	}
	raw, err := json.Marshal(map[string]any{"_scroll_id": "cursor", "hits": map[string]any{"hits": hits}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestTopologyMembersReadAllPagesAndDeduplicatePaths(t *testing.T) {
	first := []map[string]any{}
	for i := 0; i < 200; i++ {
		first = append(first, membershipFixture(fmt.Sprintf("path-%d", i), fmt.Sprint(i)))
	}
	calls, clears := 0, 0
	client, err := NewClient(ClientConfig{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete {
			clears++
			return topologyResponse(`{"succeeded":true}`), nil
		}
		calls++
		if calls == 1 {
			return topologyResponse(targetNodeResponse), nil
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if calls == 2 {
			if strings.Contains(string(body), `"track_total_hits":false`) {
				return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{"error":"disabling track_total_hits is not allowed in a scroll context"}`))}, nil
			}
			for _, value := range []string{`"bk_tenant_id":"t"`, `"bk_biz_id":2`, `"topology_ancestor_unique_ids":"module-1"`} {
				if !strings.Contains(string(body), value) {
					t.Fatalf("scope missing %s", body)
				}
			}
			return topologyResponse(scrollFixture(t, first)), nil
		}
		if calls == 3 {
			if req.URL.Path != "/_search/scroll" || !strings.Contains(string(body), `"scroll_id":"cursor"`) {
				t.Fatalf("bad scroll %s", body)
			}
			return topologyResponse(scrollFixture(t, []map[string]any{membershipFixture("second-path", "0"), membershipFixture("last-path", "200")})), nil
		}
		t.Fatal("unexpected request")
		return nil, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	refs, found, err := client.Members(t.Context(), "t", 2, "cw-Host", "module-1")
	if err != nil || !found || len(refs) != 201 || clears != 1 || calls != 3 {
		t.Fatalf("members=%d found=%t calls=%d clears=%d err=%v", len(refs), found, calls, clears, err)
	}
}

func TestTopologyMembersRejectPartialForeignAndDuplicateResponses(t *testing.T) {
	foreign := membershipFixture("path", "1")
	foreign["bk_tenant_id"] = "other"
	bodies := []string{`{"_scroll_id":"cursor","timed_out":true,"hits":{"hits":[]}}`, `{"_scroll_id":"cursor","_shards":{"failed":1},"hits":{"hits":[]}}`, `{"_scroll_id":"cursor"}`, scrollFixture(t, []map[string]any{foreign}), scrollFixture(t, []map[string]any{membershipFixture("same", "1"), membershipFixture("same", "1")})}
	for _, body := range bodies {
		calls, clears := 0, 0
		client, _ := NewClient(ClientConfig{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodDelete {
				clears++
				return topologyResponse(`{}`), nil
			}
			calls++
			if calls == 1 {
				return topologyResponse(targetNodeResponse), nil
			}
			return topologyResponse(body), nil
		})})
		refs, _, err := client.Members(t.Context(), "t", 2, "cw-Host", "module-1")
		if err == nil || len(refs) != 0 || clears != 1 {
			t.Fatalf("unsafe partial %d %d %v", len(refs), clears, err)
		}
	}
	client, _ := NewClient(ClientConfig{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsupported target reached host storage")
		return nil, nil
	})})
	if _, _, err := client.Members(t.Context(), "t", 2, "cw-Service", "module-1"); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatal(err)
	}
}

func TestTopologyMembersRejectAccumulatedPayloadAndReleaseScroll(t *testing.T) {
	// 每页合法且少于 1 MiB、总行数少于 10000，但完整拓扑投影超过调用级 32 MiB 预算。
	// 仅靠单页限制会在 Scroll 期间持续累积大型祖先路径，不能将前面的部分成员交给策略。
	pages, clears := 0, 0
	client, err := NewClient(ClientConfig{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete {
			clears++
			return topologyResponse(`{"succeeded":true}`), nil
		}
		if strings.Contains(req.URL.Path, "cmdb_biz_topo_node") {
			return topologyResponse(targetNodeResponse), nil
		}
		rows := []map[string]any{}
		if pages < 42 {
			for i := 0; i < 200; i++ {
				id := fmt.Sprint(pages*200 + i)
				row := membershipFixture("path-"+id, id)
				row["topology_ancestor_unique_ids"] = []string{"module-1", strings.Repeat("x", 4000)}
				rows = append(rows, row)
			}
		}
		pages++
		return topologyResponse(scrollFixture(t, rows)), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	refs, found, err := client.Members(t.Context(), "t", 2, "cw-Host", "module-1")
	if !errors.Is(err, ErrResultLimit) || found || len(refs) != 0 || clears != 1 {
		t.Fatalf("members=%d found=%t pages=%d clears=%d err=%v", len(refs), found, pages, clears, err)
	}
}
