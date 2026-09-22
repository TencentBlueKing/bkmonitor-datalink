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
			body = `{"hits":{"hits":[{"_source":{"bk_tenant_id":"t","producer":"cmdb_fact","target_entity_uid":"cw-Host|1","source_entity_uid":"cw-Biz|2","source_model_id":"cw-Biz","relation_identity":"belongs"}}]}}`
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
