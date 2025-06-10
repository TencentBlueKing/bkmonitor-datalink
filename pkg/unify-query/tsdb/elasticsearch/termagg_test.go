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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestBuildLabelMapFromQuery(t *testing.T) {
	testCases := []struct {
		name     string
		query    *metadata.Query
		expected map[string]*structured.LabelMapEntry
	}{
		{
			name: "QueryString only",
			query: &metadata.Query{
				QueryString: "service:web",
			},
			expected: map[string]*structured.LabelMapEntry{
				"es_inc:service:5577a3d3": {
					Values: []string{"web"},
				},
				"hl:service": {
					Values: []string{"web"},
				},
			},
		},
		{
			name: "AllConditions only",
			query: &metadata.Query{
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "status",
							Value:         []string{"error"},
							Operator:      structured.ConditionEqual,
						},
					},
				},
			},
			expected: map[string]*structured.LabelMapEntry{
				"es_inc:status:ae41e896": {
					Values: []string{"error"},
				},
				"hl:status": {
					Values: []string{"error"},
				},
			},
		},
		{
			name: "QueryString and AllConditions combined",
			query: &metadata.Query{
				QueryString: "service:web",
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "status",
							Value:         []string{"error"},
							Operator:      structured.ConditionEqual,
						},
						{
							DimensionName: "level",
							Value:         []string{"debug"},
							Operator:      structured.ConditionNotEqual,
						},
					},
				},
			},
			expected: map[string]*structured.LabelMapEntry{
				"es_inc:service:5577a3d3": {
					Values: []string{"web"},
				},
				"hl:service": {
					Values: []string{"web"},
				},
				"es_inc:status:ae41e896": {
					Values: []string{"error"},
				},
				"hl:status": {
					Values: []string{"error"},
				},
				"es_exc:level:8184a51c": {
					Values: []string{"debug"},
				},
				"hl:level": {
					Values: []string{"debug"},
				},
			},
		},
		{
			name: "Empty query",
			query: &metadata.Query{
				QueryString:   "",
				AllConditions: metadata.AllConditions{},
			},
			expected: map[string]*structured.LabelMapEntry{},
		},
		{
			name:     "Nil query",
			query:    nil,
			expected: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := buildLabelMapFromQuery(tc.query)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestFormatFactory_WithLabelMap_TermAgg(t *testing.T) {
	ctx := context.Background()
	labelMap := map[string]*structured.LabelMapEntry{
		"es_inc:status:ae41e896": {
			Values: []string{"error"},
		},
		"es_exc:level:8184a51c": {
			Values: []string{"debug"},
		},
		"hl:status": {
			Values: []string{"error"},
		},
		"hl:level": {
			Values: []string{"debug"},
		},
	}
	factory := NewFormatFactory(ctx).WithLabelMap(labelMap)

	assert.Equal(t, labelMap, factory.labelMap)

	factory.termAgg("status", true)
	factory.termAgg("level", false)

	assert.Len(t, factory.aggInfoList, 4) // 2个termAgg + 2个nestedAgg

	termAgg1, ok := factory.aggInfoList[0].(TermAgg)
	assert.True(t, ok)
	assert.Equal(t, "status", termAgg1.Name)

	termAgg2, ok := factory.aggInfoList[2].(TermAgg)
	assert.True(t, ok)
	assert.Equal(t, "level", termAgg2.Name)
}

func TestFormatFactory_Agg_WithLabelMap(t *testing.T) {
	ctx := context.Background()

	labelMap := map[string]*structured.LabelMapEntry{
		"es_inc:status:ae41e896": {
			Values: []string{"error", "warning"},
		},
		"es_exc:level:8184a51c": {
			Values: []string{"debug"},
		},
	}

	factory := NewFormatFactory(ctx).
		WithLabelMap(labelMap).
		WithMappings(map[string]any{
			"properties": map[string]any{
				"status": map[string]any{"type": "keyword"},
				"level":  map[string]any{"type": "keyword"},
			},
		})

	factory.termAgg("status", true)
	factory.termAgg("level", false)

	name, agg, err := factory.Agg()
	assert.NoError(t, err)
	assert.NotNil(t, agg)
	assert.NotEmpty(t, name)

	t.Logf("Generated aggregation name: %s", name)
}
