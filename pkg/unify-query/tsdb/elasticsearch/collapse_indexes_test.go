// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestFilterCollapseIndexes(t *testing.T) {
	for _, tt := range []struct {
		name                    string
		targets, physical, want []string
		fields                  map[string]map[string]bool
		field                   string
		empty, fail             bool
	}{
		{name: "wildcard aliases preserve scope", targets: []string{"logs*"}, physical: []string{"bad", "good"}, fields: map[string]map[string]bool{"good": {"session": true}}, field: "session", want: []string{"good"}},
		{name: "unrestricted exact alias", targets: []string{"logs"}, physical: []string{"bad", "good"}, fields: map[string]map[string]bool{"good": {"session": true}}, field: "session", want: []string{"good"}},
		{name: "physical indices", targets: []string{"bad", "good"}, physical: []string{"bad", "good"}, fields: map[string]map[string]bool{"good": {"session": true}}, field: "session", want: []string{"good"}},
		{name: "all mapped exact alias unchanged", targets: []string{"logs"}, physical: []string{"good"}, fields: map[string]map[string]bool{"good": {"session": true}}, field: "session", want: []string{"logs"}},
		{name: "mixed exact alias with unknown scope fails closed", targets: []string{"logs"}, physical: []string{"bad", "good"}, fields: map[string]map[string]bool{"good": {"session": true}}, field: "session", fail: true},
		{name: "all missing retains scope with match none", targets: []string{"logs*"}, physical: []string{"bad"}, fields: map[string]map[string]bool{"bad": {}}, field: "session", want: []string{"logs*"}, empty: true},
		{name: "no physical indices", targets: []string{"logs*"}, fields: map[string]map[string]bool{}, field: "session", want: []string{"logs*"}, empty: true},
		{name: "unknown metadata fails closed", targets: []string{"logs*"}, physical: []string{"good"}, field: "session", fail: true},
		{name: "non collapse unchanged", targets: []string{"logs*"}, physical: []string{"bad"}, want: []string{"logs*"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			qo := &queryOption{indexes: tt.targets, physicalIndexes: tt.physical}
			source := elastic.NewSearchSource().Query(elastic.NewMatchAllQuery()).Sort("timestamp", false).Collapse(elastic.NewCollapseBuilder("session"))
			var snapshot *collapseIndexMetadata
			if tt.fields != nil {
				snapshot = &collapseIndexMetadata{fields: tt.fields, directQuerySafe: map[string]bool{"good": !tt.fail}}
			}
			err := filterCollapseIndexes(qo, snapshot, tt.field, source)
			if tt.fail {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, qo.indexes)
			if tt.empty {
				body, err := marshalSearchSource(source)
				require.NoError(t, err)
				require.JSONEq(t, `{"query":{"match_none":{}},"size":0}`, body)
			}
		})
	}
}

func TestMappingFieldNames(t *testing.T) {
	var mapping map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"mappings":{"_doc":{"properties":{"attributes":{"properties":{"gen_ai.session.id":{"type":"keyword"}}},"message":{"type":"text","fields":{"keyword":{"type":"keyword"}}},"session_alias":{"type":"alias","path":"attributes.gen_ai.session.id"}},"runtime":{"computed":{"type":"keyword"}}}}}`), &mapping))
	fields := mappingFieldNames(mapping)
	for _, field := range []string{"attributes.gen_ai.session.id", "message.keyword", "session_alias", "computed"} {
		require.True(t, fields[field], field)
	}
	require.False(t, fields["absent"])
	clone := cloneIndexFields(map[string]map[string]bool{"index": fields})
	delete(clone["index"], "session_alias")
	require.True(t, fields["session_alias"])
}

func TestPrepareRawQueryFiltersCollapseIndexes(t *testing.T) {
	mock.Init()
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	calls := 0
	httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/collapse_%2A", func(*http.Request) (*http.Response, error) {
		calls++
		return httpmock.NewStringResponse(200, `{"bad":{"mappings":{"properties":{"end_time":{"type":"long"}}}},"good":{"aliases":{"collapse_good":{}},"mappings":{"properties":{"end_time":{"type":"long"},"attributes":{"properties":{"gen_ai.session.id":{"type":"keyword"}}}}}}}`), nil
	})
	inst, err := NewInstance(ctx, &InstanceOption{Connect: Connect{Address: mock.EsUrl}, Timeout: time.Second})
	require.NoError(t, err)
	q := &metadata.Query{DB: "collapse_*", StorageType: metadata.ElasticsearchStorageType, Size: 20, TimeField: metadata.TimeField{Name: "end_time", Type: "long", Unit: "microsecond"}, FieldAlias: metadata.FieldAlias{"session": "attributes.gen_ai.session.id"}, Collapse: &metadata.Collapse{Field: "session"}}
	start, end := time.Unix(1, 0), time.Unix(2, 0)
	fields, err := inst.PrepareRawFieldMetadata(ctx, q, start, end)
	require.NoError(t, err)
	prepared, err := inst.PrepareRawQuery(ctx, q, start, end, fields)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, []string{"good"}, prepared.queryOption.indexes)
	require.Contains(t, prepared.body, `"collapse":{"field":"attributes.gen_ai.session.id"}`)
	require.Equal(t, []string{"collapse_*"}, fields.indexes)
	require.Equal(t, "session", q.Collapse.Field)
	encoded, err := encodeRawBatchMember(RawBatchMember{Prepared: prepared})
	require.NoError(t, err)
	require.Contains(t, encoded, `"index":["good"]`)
	httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/good/_search", httpmock.NewStringResponder(200, `{"took":1,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`))
	_, _, _, err = inst.QueryPreparedRawData(ctx, prepared, make(chan map[string]any, 1))
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestCollapseDirectQuerySafe(t *testing.T) {
	for _, tt := range []struct {
		name    string
		targets []string
		aliases map[string]any
		want    bool
	}{
		{name: "unfiltered wildcard alias", targets: []string{"logs*"}, aliases: map[string]any{"logs_today": map[string]any{}}, want: true},
		{name: "unfiltered exact alias", targets: []string{"logs_today"}, aliases: map[string]any{"logs_today": map[string]any{}}, want: true},
		{name: "filtered wildcard alias", targets: []string{"logs*"}, aliases: map[string]any{"logs_today": map[string]any{"filter": map[string]any{"term": map[string]any{"tenant": "a"}}}}},
		{name: "search routing", targets: []string{"logs*"}, aliases: map[string]any{"logs_today": map[string]any{"search_routing": "1"}}},
		{name: "routing shorthand", targets: []string{"logs*"}, aliases: map[string]any{"logs_today": map[string]any{"routing": "1"}}},
		{name: "index routing only", targets: []string{"logs*"}, aliases: map[string]any{"logs_today": map[string]any{"index_routing": "1"}}, want: true},
		{name: "unrelated filtered alias", targets: []string{"logs*"}, aliases: map[string]any{"logs_today": map[string]any{}, "other": map[string]any{"filter": map[string]any{}}}, want: true},
		{name: "metadata missing", targets: []string{"logs*"}},
		{name: "explicit physical index", targets: []string{"v2_good"}, want: true},
		{name: "physical pattern", targets: []string{"v2_*"}, want: true},
		{name: "negative expression fails closed", targets: []string{"v2_*", "-other"}},
		{name: "no regex expansion", targets: []string{"logs[ab]"}, aliases: map[string]any{"logsa": map[string]any{}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, collapseDirectQuerySafe("v2_good", tt.aliases, tt.targets))
		})
	}
}

func TestCloneCollapseIndexMetadata(t *testing.T) {
	original := &collapseIndexMetadata{fields: map[string]map[string]bool{"good": {"session": true}}, directQuerySafe: map[string]bool{"good": true}}
	cloned := cloneCollapseIndexMetadata(original)
	cloned.fields["good"]["session"] = false
	cloned.directQuerySafe["good"] = false
	require.True(t, original.fields["good"]["session"])
	require.True(t, original.directQuerySafe["good"])
}
