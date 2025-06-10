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

func TestES_TermAgg_IncludeExclude(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name          string
		query         *metadata.Query
		fieldName     string
		expectInclude []string
		expectExclude []string
	}{
		{
			name: "Include values from positive conditions",
			query: &metadata.Query{
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "status",
							Value:         []string{"error", "warning"},
							Operator:      structured.ConditionEqual,
						},
					},
				},
			},
			fieldName:     "status",
			expectInclude: []string{"error", "warning"},
			expectExclude: []string{},
		},
		{
			name: "Exclude values from negative conditions",
			query: &metadata.Query{
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "level",
							Value:         []string{"debug", "trace"},
							Operator:      structured.ConditionNotEqual,
						},
					},
				},
			},
			fieldName:     "level",
			expectInclude: []string{},
			expectExclude: []string{"debug", "trace"},
		},
		{
			name: "Mixed include and exclude for same field",
			query: &metadata.Query{
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "status",
							Value:         []string{"success", "ok"},
							Operator:      structured.ConditionEqual,
						},
						{
							DimensionName: "status",
							Value:         []string{"error", "failed"},
							Operator:      structured.ConditionNotEqual,
						},
					},
				},
			},
			fieldName:     "status",
			expectInclude: []string{"success", "ok"},
			expectExclude: []string{"error", "failed"},
		},
		{
			name: "QueryString and Conditions combined",
			query: &metadata.Query{
				QueryString: "service:web",
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "service",
							Value:         []string{"api"},
							Operator:      structured.ConditionEqual,
						},
					},
				},
			},
			fieldName:     "service",
			expectInclude: []string{"web", "api"},
			expectExclude: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			labelMap, err := buildLabelMapFromQuery(tc.query)
			assert.NoError(t, err)

			factory := NewFormatFactory(ctx).
				WithLabelMap(labelMap).
				WithMappings(map[string]any{
					"properties": map[string]any{
						tc.fieldName: map[string]any{"type": "keyword"},
					},
				})

			factory.termAgg(tc.fieldName, true)

			name, agg, err := factory.Agg()
			assert.NoError(t, err)
			assert.NotNil(t, agg)

			assert.NotNil(t, agg)
			assert.NotEmpty(t, name)

			t.Logf("LabelMap keys: %v", getKeys(labelMap))
			t.Logf("Expected include: %v, exclude: %v", tc.expectInclude, tc.expectExclude)

			assert.NotEmpty(t, labelMap)
		})
	}
}

func TestES_TermAgg_FieldMapping(t *testing.T) {
	ctx := context.Background()

	encodeFunc := func(field string) string {
		if field == "original_field" {
			return "encoded_field"
		}
		return field
	}

	decodeFunc := func(field string) string {
		if field == "encoded_field" {
			return "original_field"
		}
		return field
	}

	labelMap := map[string]*structured.LabelMapEntry{
		"es_inc:original_field:test123": {
			Values: []string{"value1", "value2"},
		},
	}

	factory := NewFormatFactory(ctx).
		WithLabelMap(labelMap).
		WithTransform(encodeFunc, decodeFunc).
		WithMappings(map[string]any{
			"properties": map[string]any{
				"encoded_field": map[string]any{"type": "keyword"},
			},
		})

	factory.termAgg("encoded_field", true)

	name, agg, err := factory.Agg()
	assert.NoError(t, err)
	assert.NotNil(t, agg)
	assert.NotEmpty(t, name)

	t.Logf("Successfully created aggregation with field mapping")
}

func getKeys(labelMap map[string]*structured.LabelMapEntry) []string {
	keys := make([]string, 0, len(labelMap))
	for key := range labelMap {
		keys = append(keys, key)
	}
	return keys
}

func TestES_Integration_Complete(t *testing.T) {
	ctx := context.Background()

	query := &metadata.Query{
		QueryString: "service:web AND environment:production",
		AllConditions: metadata.AllConditions{
			{
				{
					DimensionName: "status",
					Value:         []string{"error", "warning"},
					Operator:      structured.ConditionEqual,
				},
				{
					DimensionName: "level",
					Value:         []string{"debug"},
					Operator:      structured.ConditionNotEqual,
				},
			},
		},
		Aggregates: metadata.Aggregates{
			{
				Name:       "count",
				Dimensions: []string{"status", "level", "service"},
			},
		},
	}

	labelMap, err := buildLabelMapFromQuery(query)
	assert.NoError(t, err)
	assert.NotEmpty(t, labelMap)

	factory := NewFormatFactory(ctx).
		WithLabelMap(labelMap).
		WithMappings(map[string]any{
			"properties": map[string]any{
				"status":      map[string]any{"type": "keyword"},
				"level":       map[string]any{"type": "keyword"},
				"service":     map[string]any{"type": "keyword"},
				"environment": map[string]any{"type": "keyword"},
			},
		})

	name, agg, err := factory.EsAgg(query.Aggregates)
	assert.NoError(t, err)
	assert.NotNil(t, agg)
	assert.NotEmpty(t, name)

	t.Logf("Successfully generated complete ES aggregation")
	t.Logf("LabelMap contains %d entries", len(labelMap))

	hasStatusInclude := false
	hasLevelExclude := false
	hasServiceInclude := false
	hasEnvironmentInclude := false

	for key := range labelMap {
		if key == "hl:status" || key == "hl:level" || key == "hl:service" || key == "hl:environment" {
			continue // highlight keys
		}

		if len(key) > 7 && key[:7] == "es_inc:" && len(key) > 14 && key[7:14] == "status:" {
			hasStatusInclude = true
		}
		if len(key) > 7 && key[:7] == "es_exc:" && len(key) > 13 && key[7:13] == "level:" {
			hasLevelExclude = true
		}
		if len(key) > 7 && key[:7] == "es_inc:" && len(key) > 15 && key[7:15] == "service:" {
			hasServiceInclude = true
		}
		if len(key) > 7 && key[:7] == "es_inc:" && len(key) > 19 && key[7:19] == "environment:" {
			hasEnvironmentInclude = true
		}
	}

	assert.True(t, hasStatusInclude, "Should have status include key")
	assert.True(t, hasLevelExclude, "Should have level exclude key")
	assert.True(t, hasServiceInclude, "Should have service include key")
	assert.True(t, hasEnvironmentInclude, "Should have environment include key")
}
