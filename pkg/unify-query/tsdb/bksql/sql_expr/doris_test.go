// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sql_expr

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestDorisSQLExpr_ParserQueryString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		err   string
	}{
		{
			name:  "simple match",
			input: "stringField:test",
			want:  "`stringField` = 'test'",
		},
		{
			name:  "one word",
			input: "test",
			want:  "`log` = 'test'",
			// err:   "doris 不支持全字段检索: test",
		},
		{
			name:  "complex nested query",
			input: "(s1:1 AND (s2:2 OR s3:3)) OR NOT s4:4",
			want:  "(`s1` = '1' AND (`s2` = '2' OR `s3` = '3')) OR `s4` != '4'",
		},
		{
			name:  "trailing operators ignored",
			input: "stringField:test AND OR",
			want:  "`stringField` = 'test'",
		},
		{
			name:  "empty input",
			input: "",
		},
		{
			name:  "OR expression with multiple terms",
			input: "s1:1 OR s2:2 OR s3:3",
			want:  "`s1` = '1' OR `s2` = '2' OR `s3` = '3'",
		},
		{
			name:  "mixed AND/OR with proper precedence",
			input: "s1:1 AND s2:2 OR s3:3",
			want:  "`s1` = '1' AND `s2` = '2' OR `s3` = '3'",
		},
		{
			name:  "exact match with quotes",
			input: "stringField:\"exact match\"",
			want:  "`stringField` = 'exact match'",
		},
		{
			name:  "numeric equality",
			input: "s1:25",
			want:  "`s1` = '25'",
		},
		{
			name:  "date range query",
			input: "s1:[2023-01-01 TO 2023-12-31]",
			want:  "`s1` >= '2023-01-01' AND `s1` <= '2023-12-31'",
		},
		{
			name:  "invalid field name",
			input: "s1:value",
			want:  "`s1` = 'value'",
		},
		{
			name:  "text filter",
			input: "ts1:value",
			want:  "`ts1` MATCH_PHRASE 'value'",
		},
		{
			name:  "object field",
			input: "object.field: value",
			want:  "CAST(object['field'] AS STRING) = 'value'",
		},
		{
			name:  "start",
			input: "s1: >100",
			want:  "`s1` > '100'",
		},
		{
			name:  "start-2",
			input: "s1:>=100",
			want:  "`s1` >= '100'",
		},
		{
			name:  "end",
			input: "s1: <100",
			want:  "`s1` < '100'",
		},
		{
			name:  "end-2",
			input: "s1:<=100",
			want:  "`s1` <= '100'",
		},
		{
			name:  "array string",
			input: `events.attributes.exception.type: "error"`,
			want:  "CAST(events['attributes']['exception.type'] AS TEXT ARRAY) = 'error'",
		},
	}

	ctx := metadata.InitHashID(context.Background())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)

			got, err := NewSQLExpr(Doris).WithFieldsMap(metadata.FieldsMap{
				"text":                             {FieldType: DorisTypeText, IsAnalyzed: true},
				"events.attributes.exception.type": {FieldType: fmt.Sprintf(DorisTypeArray, DorisTypeText)},
				"stringField":                      {FieldType: DorisTypeString},
				"s1":                               {FieldType: DorisTypeString},
				"s2":                               {FieldType: DorisTypeString},
				"s3":                               {FieldType: DorisTypeString},
				"s4":                               {FieldType: DorisTypeString},
				"ts1":                              {FieldType: DorisTypeString, IsAnalyzed: true},
				"log":                              {FieldType: DorisTypeString},
				"object.field":                     {FieldType: DorisTypeString},
			}).WithEncode(func(s string) string {
				return fmt.Sprintf("`%s`", s)
			}).ParserQueryString(ctx, tt.input)
			if err != nil {
				assert.Equal(t, tt.err, err.Error())
				return
			}

			assert.Equal(t, tt.want, got)
		})
	}
}

// TestDorisSQLExpr_ParserAllConditions 单元测试
func TestDorisSQLExpr_ParserAllConditions(t *testing.T) {
	tests := []struct {
		name      string
		condition metadata.AllConditions
		want      string
		wantErr   error
	}{
		{
			name: "doris test multi object field condition",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "object.field.name",
						Value:         []string{"What's UP"},
						Operator:      metadata.ConditionEqual,
					},
					{
						DimensionName: "tag.city.town.age",
						Value:         []string{"test"},
						Operator:      metadata.ConditionNotEqual,
					},
				},
			},
			want: `CAST(object['field']['name'] AS STRING) = 'What''s UP' AND CAST(tag['city']['town']['age'] AS TINYINT) != 'test'`,
		},
		{
			name: "doris test object field condition",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "object.field",
						Value:         []string{"What's UP"},
						Operator:      metadata.ConditionContains,
					},
				},
				{
					{
						DimensionName: "object.field",
						Value:         []string{"What's UP"},
						Operator:      metadata.ConditionEqual,
					},
					{
						DimensionName: "tag",
						Value:         []string{"test"},
						Operator:      metadata.ConditionNotEqual,
					},
				},
			},
			want: "(CAST(object['field'] AS TEXT) MATCH_PHRASE 'What''s UP' OR CAST(object['field'] AS TEXT) = 'What''s UP' AND `tag` != 'test')",
		},
		{
			name: "doris test object field condition",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "object.field",
						Value:         []string{"What's UP"},
						Operator:      metadata.ConditionEqual,
						IsPrefix:      true,
					},
					{
						DimensionName: "tag",
						Value:         []string{"test"},
						Operator:      metadata.ConditionNotEqual,
						IsSuffix:      true,
					},
				},
			},
			want: "CAST(object['field'] AS TEXT) MATCH_PHRASE_PREFIX 'What''s UP' AND `tag` NOT MATCH_PHRASE_EDGE 'test'",
		},
		{
			name: "doris t8est text field wildcard use *",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "object.field",
						Value:         []string{"*partial*"},
						Operator:      metadata.ConditionContains,
						IsWildcard:    true,
					},
				},
			},
			want: "CAST(object['field'] AS TEXT) LIKE '%partial%'",
		},
		{
			name: "doris t8est text field wildcard use *",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "object.field",
						Value:         []string{"*pa*tial*"},
						Operator:      metadata.ConditionContains,
						IsWildcard:    true,
					},
				},
			},
			want: "CAST(object['field'] AS TEXT) LIKE '%pa%tial%'",
		},
		{
			name: "doris t8est text field wildcard use ?",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "object.field",
						Value:         []string{"*pa*tial?"},
						Operator:      metadata.ConditionContains,
						IsWildcard:    true,
					},
				},
			},
			want: "CAST(object['field'] AS TEXT) LIKE '%pa%tial_'",
		},
		{
			name: "doris t8est text field wildcard use ?",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "object.field",
						Value:         []string{"*pa\\*tial?"},
						Operator:      metadata.ConditionContains,
						IsWildcard:    true,
					},
				},
			},
			want: "CAST(object['field'] AS TEXT) LIKE '%pa\\*tial_'",
		},
		{
			name: "doris test OR condition",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "status",
						Value:         []string{"running"},
						Operator:      metadata.ConditionEqual,
					},
				},
				{
					{
						DimensionName: "code",
						Value:         []string{"500"},
						Operator:      metadata.ConditionEqual,
					},
				},
			},
			want: "(`status` = 'running' OR `code` = '500')",
		},
		{
			name: "doris test numeric field without cast",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "cpu_usage",
						Value:         []string{"80"},
						Operator:      metadata.ConditionGt,
					},
				},
			},
			want: "`cpu_usage` > 80",
		},
		{
			name: "test IN operator",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "env",
						Value:         []string{"prod", "test"},
						Operator:      metadata.ConditionContains,
					},
				},
			},
			want: "`env` IN ('prod', 'test')",
		},
		{
			name: "test IN operator with wildcard and no *",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "env",
						Value:         []string{"prod", "test"},
						Operator:      metadata.ConditionContains,
						IsWildcard:    true,
					},
				},
			},
			want: "(`env` LIKE 'prod' OR `env` LIKE 'test')",
		},
		{
			name: "doris test empty value",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "host",
						Value:         []string{},
						Operator:      metadata.ConditionEqual,
					},
				},
			},
			want: "",
		},
		{
			name: "doris test invalid operator",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "time",
						Value:         []string{"2023"},
						Operator:      "unknown",
					},
				},
			},
			wantErr: fmt.Errorf("unknown operator unknown"),
		},
		{
			name: "doris array text eq",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "events.attributes.exception.type",
						Value:         []string{"errorString"},
						Operator:      "ne",
						IsWildcard:    false,
					},
				},
			},
			want: `ARRAY_CONTAINS(CAST(events['attributes']['exception.type'] AS TEXT ARRAY), 'errorString') != 1`,
		},
		{
			name: "doris 条件合并",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "gseIndex",
						Value: []string{
							"101010",
						},
						Operator: "lt",
					}, {
						DimensionName: "serverIp",
						Value: []string{
							"127.0.0.1",
						},
						Operator: "eq",
					}, {
						DimensionName: "path",
						Value: []string{
							"/var/host/data/bcs/lib/docker/containers/npc/npc-json.log",
						},
						Operator: "eq",
					}, {
						DimensionName: "__ext.container_id",
						Value: []string{
							"npc",
						},
						Operator: "eq",
					},
				},
				{
					{
						DimensionName: "gseIndex",
						Value: []string{
							"101010",
						},
						Operator: "eq",
					}, {
						DimensionName: "iterationIndex",
						Value: []string{
							"11",
						},
						Operator: "lt",
					}, {
						DimensionName: "serverIp",
						Value: []string{
							"127.0.0.1",
						},
						Operator: "eq",
					}, {
						DimensionName: "path",
						Value: []string{
							"/var/host/data/bcs/lib/docker/containers/npc/npc-json.log",
						},
						Operator: "eq",
					}, {
						DimensionName: "__ext.container_id",
						Value: []string{
							"npc",
						},
						Operator: "eq",
					},
				},
				{
					{
						DimensionName: "gseIndex",
						Value: []string{
							"101010",
						},
						Operator: "eq",
					}, {
						DimensionName: "iterationIndex",
						Value: []string{
							"11",
						},
						Operator: "eq",
					}, {
						DimensionName: "dtEventTimeStamp",
						Value: []string{
							"1760514288000",
						},
						Operator: "lt",
					}, {
						DimensionName: "serverIp",
						Value: []string{
							"127.0.0.1",
						},
						Operator: "eq",
					}, {
						DimensionName: "path",
						Value: []string{
							"/var/host/data/bcs/lib/docker/containers/npc/npc-json.log",
						},
						Operator: "eq",
					}, {
						DimensionName: "__ext.container_id",
						Value: []string{
							"npc",
						},
						Operator: "eq",
					},
				},
			},
			want: "`serverIp` = '127.0.0.1' AND `path` = '/var/host/data/bcs/lib/docker/containers/npc/npc-json.log' AND CAST(__ext['container_id'] AS STRING) = 'npc' AND (`gseIndex` < 101010 OR `gseIndex` = '101010' AND `iterationIndex` < 11 OR `gseIndex` = '101010' AND `iterationIndex` = '11' AND `dtEventTimeStamp` < 1760514288000)",
		},
		{
			name: "doris 条件合并 - 有个全都是公共条件",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "serverIp",
						Value: []string{
							"127.0.0.1",
						},
						Operator: "eq",
					}, {
						DimensionName: "path",
						Value: []string{
							"/var/host/data/bcs/lib/docker/containers/npc/npc-json.log",
						},
						Operator: "eq",
					}, {
						DimensionName: "__ext.container_id",
						Value: []string{
							"npc",
						},
						Operator: "eq",
					},
				},
				{
					{
						DimensionName: "gseIndex",
						Value: []string{
							"101010",
						},
						Operator: "lt",
					}, {
						DimensionName: "serverIp",
						Value: []string{
							"127.0.0.1",
						},
						Operator: "eq",
					}, {
						DimensionName: "path",
						Value: []string{
							"/var/host/data/bcs/lib/docker/containers/npc/npc-json.log",
						},
						Operator: "eq",
					}, {
						DimensionName: "__ext.container_id",
						Value: []string{
							"npc",
						},
						Operator: "eq",
					},
				},
				{
					{
						DimensionName: "gseIndex",
						Value: []string{
							"101010",
						},
						Operator: "eq",
					},
					{
						DimensionName: "iterationIndex",
						Value: []string{
							"11",
						},
						Operator: "lt",
					},
					{
						DimensionName: "serverIp",
						Value: []string{
							"127.0.0.1",
						},
						Operator: "eq",
					},
					{
						DimensionName: "path",
						Value: []string{
							"/var/host/data/bcs/lib/docker/containers/npc/npc-json.log",
						},
						Operator: "eq",
					},
					{
						DimensionName: "__ext.container_id",
						Value: []string{
							"npc",
						},
						Operator: "eq",
					},
				},
				{
					{
						DimensionName: "gseIndex",
						Value: []string{
							"101010",
						},
						Operator: "eq",
					}, {
						DimensionName: "iterationIndex",
						Value: []string{
							"11",
						},
						Operator: "eq",
					}, {
						DimensionName: "dtEventTimeStamp",
						Value: []string{
							"1760514288000",
						},
						Operator: "lt",
					}, {
						DimensionName: "serverIp",
						Value: []string{
							"127.0.0.1",
						},
						Operator: "eq",
					}, {
						DimensionName: "path",
						Value: []string{
							"/var/host/data/bcs/lib/docker/containers/npc/npc-json.log",
						},
						Operator: "eq",
					}, {
						DimensionName: "__ext.container_id",
						Value: []string{
							"npc",
						},
						Operator: "eq",
					},
				},
			},
			want: "`serverIp` = '127.0.0.1' AND `path` = '/var/host/data/bcs/lib/docker/containers/npc/npc-json.log' AND CAST(__ext['container_id'] AS STRING) = 'npc'",
		},
	}

	e := NewSQLExpr(Doris).WithFieldsMap(metadata.FieldsMap{
		"object.field":                     {FieldType: DorisTypeText},
		"object.field.name":                {FieldType: DorisTypeString},
		"tag.city.town.age":                {FieldType: DorisTypeTinyInt},
		"events.attributes.exception.type": {FieldType: fmt.Sprintf(DorisTypeArray, DorisTypeText)},
		"events.timestamp":                 {FieldType: fmt.Sprintf(DorisTypeArray, DorisTypeBigInt)},
		"text": {
			FieldType:  DorisTypeText,
			IsAnalyzed: true,
		},
		"tag":                {FieldType: DorisTypeString},
		"status":             {FieldType: DorisTypeString},
		"code":               {FieldType: DorisTypeString},
		"cpu_usage":          {FieldType: DorisTypeInt},
		"env":                {FieldType: DorisTypeString},
		"serverIp":           {FieldType: DorisTypeString},
		"__ext.container_id": {FieldType: DorisTypeString},
		"path":               {FieldType: DorisTypeString},
		"gseIndex":           {FieldType: DorisTypeInt},
		"iterationIndex":     {FieldType: DorisTypeInt},
		"dtEventTimeStamp":   {FieldType: DorisTypeBigInt},
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.ParserAllConditions(tt.condition)
			if err != nil {
				assert.Equal(t, tt.wantErr, err)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
