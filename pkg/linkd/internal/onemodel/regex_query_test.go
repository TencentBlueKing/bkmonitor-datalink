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

	"linkd/internal/textregexp"
)

func TestRegexFirstMatchContinuesPastTheFirstPage(t *testing.T) {
	queries, closed := 0, 0
	client, err := NewClient(ClientConfig{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodDelete {
			closed++
			return pageResponse(`{}`), nil
		}
		if strings.HasSuffix(request.URL.Path, "/_pit") {
			return pageResponse(`{"id":"opened"}`), nil
		}
		queries++
		raw, _ := io.ReadAll(request.Body)
		if strings.Contains(string(raw), `"regexp"`) {
			t.Fatal("regexp2 expression was sent to Elasticsearch")
		}
		if queries == 1 {
			ids := make([]string, 129)
			for index := range ids {
				ids[index] = fmt.Sprintf("item-%04d", index+1)
			}
			return pageResponse(pageBody(ids...)), nil
		}
		if !strings.Contains(string(raw), `"search_after":["item-0128","instances"]`) {
			t.Fatal(string(raw))
		}
		return pageResponse(pageBody("item-0129")), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.Search(t.Context(), "t", Query{ModelID: "host", Limit: 1, First: true, Where: Filter{Field: "model_inst_id", Type: InstanceAttributeKeyword, Operator: "regex", Value: `(?<=item-)0129$`}})
	if err != nil || len(items) != 1 || items[0].InstanceID != "item-0129" || queries != 2 || closed != 1 {
		t.Fatal(items, queries, closed, err)
	}
}

func TestRegexQueryCandidateBudgetAndCursor(t *testing.T) {
	for _, tc := range []struct {
		name        string
		total       int
		pattern     string
		first       bool
		limit, want int
		err         error
	}{
		{"first after 1000", 1001, `1001$`, true, 1, 1, nil},
		{"first at budget", 1025, `1024$`, true, 1, 1, nil},
		{"complete at budget", 1024, `1024$`, false, 1, 1, nil},
		{"empty at budget", 1024, `missing`, false, 1, 0, nil},
		{"first beyond budget", 1025, `1025$`, true, 1, 0, ErrResultLimit},
		{"incomplete result", 1025, `1024$`, false, 1, 0, ErrResultLimit},
		{"result limit", 3, `item`, false, 2, 0, ErrResultLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			closed, size := 0, 0
			client, err := NewClient(ClientConfig{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodDelete {
					closed++
					return pageResponse(`{}`), nil
				}
				if strings.HasSuffix(request.URL.Path, "/_pit") {
					return pageResponse(`{"id":"opened"}`), nil
				}
				var body struct {
					Size  int      `json:"size"`
					After []string `json:"search_after"`
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if size != 0 && size != body.Size {
					t.Fatal("page size changed with an active cursor")
				}
				size = body.Size
				start := 1
				if len(body.After) > 0 {
					if _, err := fmt.Sscanf(body.After[0], "item-%d", &start); err != nil {
						t.Fatal(err)
					}
					start++
				}
				ids := []string{}
				for n := start; n < start+size && n <= tc.total; n++ {
					ids = append(ids, fmt.Sprintf("item-%04d", n))
				}
				return pageResponse(pageBody(ids...)), nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			items, err := client.Search(t.Context(), "t", Query{ModelID: "host", First: tc.first, Limit: tc.limit, Where: Filter{Field: "model_inst_id", Type: InstanceAttributeKeyword, Operator: "regex", Value: tc.pattern}})
			if !errors.Is(err, tc.err) || len(items) != tc.want || closed != 1 {
				t.Fatalf("items=%d err=%v closed=%d", len(items), err, closed)
			}
		})
	}
}

func TestRegexSiblingTypedConditions(t *testing.T) {
	pattern, err := textregexp.Compile(`never`)
	if err != nil {
		t.Fatal(err)
	}
	regex := Filter{Field: "model_inst_id", Type: InstanceAttributeKeyword, Operator: "regex", Value: "never"}
	patterns := map[string]*textregexp.Expression{"never": pattern}
	for _, tc := range []struct {
		name             string
		kind             InstanceAttributeType
		op               string
		actual, expected any
		want, invalid    bool
	}{
		{"same instant", InstanceAttributeDatetime, "eq", "2026-10-17T08:00:00+08:00", "2026-10-17T00:00:00Z", true, false},
		{"timezone order", InstanceAttributeDatetime, "gt", "2026-10-17T08:00:00+08:00", "2026-10-17T01:00:00Z", false, false},
		{"fraction order", InstanceAttributeDatetime, "gt", "2026-10-17T00:00:00.1Z", "2026-10-17T00:00:00Z", true, false},
		{"date only", InstanceAttributeDatetime, "in", "2026-10-17", []any{"2026-10-17T00:00:00Z"}, true, false},
		{"invalid date", InstanceAttributeDatetime, "eq", "invalid", "2026-10-17", false, true},
		{"IPv6 spelling", InstanceAttributeIP, "eq", "2001:0db8:0:0:0:0:0:1", "2001:db8::1", true, false},
		{"IPv4 mapped", InstanceAttributeIP, "eq", "::ffff:192.0.2.1", "192.0.2.1", true, false},
		{"IPv4 order", InstanceAttributeIP, "gt", "10.0.0.10", "10.0.0.2", true, false},
		{"IPv6 order", InstanceAttributeIP, "lt", "2001:db8::f", "2001:db8::10", true, false},
		{"invalid IP", InstanceAttributeIP, "eq", "invalid", "10.0.0.1", false, true},
		{"number", InstanceAttributeLong, "gte", json.Number("10"), json.Number("2"), true, false},
		{"keyword remains lexical", InstanceAttributeKeyword, "gt", "10", "2", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leaf := Filter{Field: "attributes.x", Type: tc.kind, Operator: tc.op, Value: tc.expected}
			instance := Instance{InstanceID: "host-1", Attributes: map[string]any{"x": tc.actual}}
			for _, negated := range []bool{false, true} {
				positive := Filter{Any: []Filter{regex, leaf}}
				filter := positive
				if negated {
					filter = Filter{Not: &positive}
				}
				got, err := matchInstanceFilter(filter, instance, patterns)
				if (err != nil) != tc.invalid || (!tc.invalid && got != (tc.want != negated)) {
					t.Fatalf("negated=%v got=%v err=%v", negated, got, err)
				}
			}
		})
	}
}

func TestRegexSiblingExistsChecksValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  bool
	}{
		{"null", nil, false}, {"empty array", []any{}, false}, {"null array", []any{nil}, false},
		{"typed empty array", []string{}, false}, {"empty text", "", true},
		{"false", false, true}, {"zero", 0, true}, {"array value", []any{nil, ""}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filter := Filter{Field: "attributes.x", Type: InstanceAttributeKeyword, Operator: "exists"}
			for _, negated := range []bool{false, true} {
				f := filter
				if negated {
					f = Filter{Not: &filter}
				}
				got, err := matchInstanceFilter(f, Instance{Attributes: map[string]any{"x": tc.value}}, nil)
				if err != nil || got != (tc.want != negated) {
					t.Fatalf("got=%v err=%v", got, err)
				}
			}
		})
	}
}

func TestRegexCandidatePruningPreservesOrAndNot(t *testing.T) {
	regular := Filter{Field: "attributes.enabled", Type: InstanceAttributeBoolean, Operator: "eq", Value: false}
	regex := Filter{Field: "attributes.name", Type: InstanceAttributeKeyword, Operator: "regex", Value: `(?<=前缀)主机`}
	pattern, err := textregexp.Compile(regex.Value.(string))
	if err != nil {
		t.Fatal(err)
	}
	patterns := map[string]*textregexp.Expression{regex.Value.(string): pattern}
	for _, filter := range []Filter{{Any: []Filter{regular, regex}}, {Not: &regex}} {
		if !regexCandidates(filter).Empty() {
			t.Fatal("unsafe candidate narrowing", filter)
		}
	}
	filter := Filter{Any: []Filter{regular, regex}}
	for _, attributes := range []map[string]any{{"enabled": false, "name": "other"}, {"enabled": true, "name": "前缀主机"}} {
		match, err := matchInstanceFilter(filter, Instance{Attributes: attributes}, patterns)
		if err != nil || !match {
			t.Fatal(attributes, match, err)
		}
	}
	and := regexCandidates(Filter{All: []Filter{regular, regex}})
	if and.Field != regular.Field || and.Value != false {
		t.Fatal(and)
	}
}
