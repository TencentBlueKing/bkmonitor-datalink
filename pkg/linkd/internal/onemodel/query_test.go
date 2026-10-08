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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTypedFilterNegationAndInvalidValues(t *testing.T) {
	filter := Filter{Field: "attributes.bk_cloud_id", Type: InstanceAttributeLong, Operator: "ne", Value: json.Number("0")}
	clause, err := filter.Compile()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(clause)
	want := `{"bool":{"must_not":[{"nested":{"path":"attribute_values","query":{"bool":{"filter":[{"term":{"attribute_values.field_name":"bk_cloud_id"}},{"term":{"attribute_values.long_values":0}}]}},"score_mode":"none"}}]}}`
	if string(encoded) != want {
		t.Fatalf("filter=%s", encoded)
	}
	for _, f := range []Filter{{Field: "attributes.x", Type: InstanceAttributeLong, Operator: "eq", Value: 1.5}, {Field: "bk_tenant_id", Type: InstanceAttributeKeyword, Operator: "eq", Value: "other"}, {Field: "attributes.x", Type: InstanceAttributeDouble, Operator: "eq", Value: "NaN"}} {
		if _, err := f.Compile(); err == nil {
			t.Fatalf("accepted %+v", f)
		}
	}
}

func TestSearchRequiresCompleteResponseEnvelope(t *testing.T) {
	for _, body := range []string{`{}`, `{"hits":{}}`, `{"hits":{"hits":null}}`, `{"hits":{"hits":[]}} {}`, `{"hits":{"hits":[{"_source":null}]}}`} {
		t.Run(body, func(t *testing.T) {
			c, err := NewClient(ClientConfig{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := c.Search(t.Context(), "t", Query{ModelID: "cw-Host", Limit: 1})
			if !errors.Is(err, ErrInvalidDataSourceResponse) || len(rows) != 0 {
				t.Fatalf("malformed response became complete result: rows=%v err=%v", rows, err)
			}
		})
	}
}

func TestSearchResponseByteBudget(t *testing.T) {
	const empty = `{"hits":{"hits":[]}}`
	for _, size := range []int{maxOneModelResponseBytes, maxOneModelResponseBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			body := empty + strings.Repeat(" ", size-len(empty))
			c, err := NewClient(ClientConfig{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := c.Search(t.Context(), "t", Query{ModelID: "cw-Host", Limit: 1})
			if len(rows) != 0 || (size == maxOneModelResponseBytes && err != nil) || (size > maxOneModelResponseBytes && !errors.Is(err, ErrResultLimit)) {
				t.Fatalf("response budget: rows=%v err=%v", rows, err)
			}
		})
	}
}

func TestRelatedCancellationCannotBecomeEmptySuccess(t *testing.T) {
	c, err := NewClient(ClientConfig{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("canceled query reached backend")
		return nil, context.Canceled
	})})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rows, err := c.Related(ctx, "t", []Instance{{TenantID: "t", ModelCode: "cw-Host", InstanceID: "1"}}, "belongs", "both", Query{ModelID: "cw-Biz", Limit: 1})
	if !errors.Is(err, context.Canceled) || len(rows) != 0 {
		t.Fatalf("cancellation swallowed: rows=%v err=%v", rows, err)
	}
}

func TestRelatedValidatesQueryBeforeEmptyResult(t *testing.T) {
	for _, tc := range []struct {
		name, tenant, relation string
		root                   Instance
		query                  Query
	}{
		{"blank tenant", " ", "belongs", Instance{TenantID: " ", ModelCode: "cw-Host", InstanceID: "1"}, Query{ModelID: "cw-Biz", Limit: 1}},
		{"empty root", "t", "belongs", Instance{TenantID: "t", ModelCode: "cw-Host"}, Query{ModelID: "cw-Biz", Limit: 1}},
		{"ambiguous root model", "t", "belongs", Instance{TenantID: "t", ModelCode: "cw|Host", InstanceID: "1"}, Query{ModelID: "cw-Biz", Limit: 1}},
		{"relation control", "t", "belongs\n", Instance{TenantID: "t", ModelCode: "cw-Host", InstanceID: "1"}, Query{ModelID: "cw-Biz", Limit: 1}},
		{"invalid limit", "t", "belongs", Instance{TenantID: "t", ModelCode: "cw-Host", InstanceID: "1"}, Query{ModelID: "cw-Biz", Limit: 1025}},
		{"invalid filter", "t", "belongs", Instance{TenantID: "t", ModelCode: "cw-Host", InstanceID: "1"}, Query{ModelID: "cw-Biz", Where: Filter{Field: "bk_tenant_id", Type: InstanceAttributeKeyword, Operator: "eq", Value: "other"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c, err := NewClient(ClientConfig{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"hits":{"hits":[]}}`))}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := c.Related(t.Context(), tc.tenant, []Instance{tc.root}, tc.relation, "both", tc.query)
			if !errors.Is(err, ErrInvalidQuery) || len(rows) != 0 || calls != 0 {
				t.Fatalf("invalid query reached backend: calls=%d rows=%v err=%v", calls, rows, err)
			}
		})
	}
}

func TestRelatedValidatesCompleteEdgesAndReturnedMembership(t *testing.T) {
	for _, tc := range []struct {
		name, fromModel, returnedID string
		wantErr                     bool
	}{
		{"valid", "cw-Host", "2", false},
		{"inconsistent origin", "cw-Other", "2", true},
		{"instance outside edge set", "cw-Host", "3", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewClient(ClientConfig{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := fmt.Sprintf(`{"hits":{"hits":[{"_source":{"bk_tenant_id":"t","producer":"cmdb_fact","source_entity_uid":"cw-Host|1","source_model_id":%q,"target_entity_uid":"cw-Biz|2","target_model_id":"cw-Biz","relation_identity":"belongs"}}]}}`, tc.fromModel)
				if strings.Contains(r.URL.Path, oneModelInstanceIndex) {
					body = fmt.Sprintf(`{"hits":{"hits":[{"_source":{"bk_tenant_id":"t","model_id":"cw-Biz","model_inst_id":%q,"entity_uid":%q,"attributes":{}}}]}}`, tc.returnedID, "cw-Biz|"+tc.returnedID)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := c.Related(t.Context(), "t", []Instance{{TenantID: "t", ModelCode: "cw-Host", InstanceID: "1"}}, "belongs", "out", Query{ModelID: "cw-Biz", Limit: 2})
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidDataSourceResponse) || len(rows) != 0 {
					t.Fatalf("partial relation accepted: rows=%v err=%v", rows, err)
				}
			} else if err != nil || len(rows) != 1 {
				t.Fatalf("rows=%v err=%v", rows, err)
			}
		})
	}
}

func TestSearchRejectsAmbiguityPartialAndTenantMismatch(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		limit      int
		wantErr    bool
	}{{"good", `{"hits":{"hits":[{"_source":{"bk_tenant_id":"t","model_id":"cw-Host","model_inst_id":"1","entity_uid":"cw-Host|1","attributes":{"x":0}}}]}}`, 1, false}, {"shards", `{"_shards":{"failed":1},"hits":{"hits":[]}}`, 1, true}, {"timeout", `{"timed_out":true,"hits":{"hits":[]}}`, 1, true}, {"tenant", `{"hits":{"hits":[{"_source":{"bk_tenant_id":"other","model_id":"cw-Host","model_inst_id":"1","entity_uid":"cw-Host|1","attributes":{}}}]}}`, 1, true}} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewClient(ClientConfig{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				b, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(b), `"bk_tenant_id":"t"`) {
					t.Fatal("missing tenant")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := c.Search(t.Context(), "t", Query{ModelID: "cw-Host", Limit: tc.limit})
			if (err != nil) != tc.wantErr {
				t.Fatalf("rows=%v err=%v", rows, err)
			}
		})
	}
}

func TestRelationDirectionAndIdentityValidation(t *testing.T) {
	calls := 0
	c, err := NewClient(ClientConfig{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		b, _ := io.ReadAll(r.Body)
		var body string
		if calls == 1 {
			if !strings.Contains(string(b), `"target_entity_uid":["cw-Host|1"]`) {
				t.Fatalf("reverse query=%s", b)
			}
			body = `{"hits":{"hits":[{"_source":{"bk_tenant_id":"t","producer":"cmdb_fact","target_entity_uid":"cw-Host|1","target_model_id":"cw-Host","source_entity_uid":"cw-Biz|2","source_model_id":"cw-Biz","relation_identity":"belongs"}}]}}`
		} else {
			body = `{"hits":{"hits":[{"_source":{"bk_tenant_id":"t","model_id":"cw-Biz","model_inst_id":"2","entity_uid":"cw-Biz|2","attributes":{}}}]}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := c.Related(context.Background(), "t", []Instance{{TenantID: "t", ModelCode: "cw-Host", InstanceID: "1"}}, "belongs", "in", Query{ModelID: "cw-Biz", Limit: 2})
	if err != nil || len(rows) != 1 || calls != 2 {
		t.Fatalf("rows=%v err=%v calls=%d", rows, err, calls)
	}
}

func TestSearchFirstPushesStableOrderAndLimitToBackend(t *testing.T) {
	c, err := NewClient(ClientConfig{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var q map[string]any
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Fatal(err)
		}
		if q["size"] != float64(1) {
			t.Fatal(q)
		}
		raw, _ := json.Marshal(q["sort"])
		if string(raw) != `[{"model_id":"asc"},{"model_inst_id":"asc"}]` {
			t.Fatal(q)
		}
		return topologySearchResponse(map[string]any{"bk_tenant_id": "t", "model_id": "cw-Host", "model_inst_id": "101", "entity_uid": "cw-Host|101", "attributes": map[string]any{}}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := c.Search(t.Context(), "t", Query{ModelID: "cw-Host", Limit: 1, First: true})
	if err != nil || len(rows) != 1 || rows[0].InstanceID != "101" {
		t.Fatal(rows, err)
	}
}
