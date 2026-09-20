// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/elasticsearch"
)

func TestRawESBatchSemanticFingerprintSeparatesDifferentConditions(t *testing.T) {
	left := rawESBatchSemanticQuery()
	right := rawESBatchSemanticQuery()
	right.AllConditions[0][0].Value = []string{"recovery"}

	leftFingerprint, err := rawESBatchSemanticFingerprint(left)
	require.NoError(t, err)
	rightFingerprint, err := rawESBatchSemanticFingerprint(right)
	require.NoError(t, err)

	assert.NotEqual(t, leftFingerprint, rightFingerprint)
}

func TestRawESBatchSemanticFingerprintIgnoresRTIdentityAndMapInsertionOrder(t *testing.T) {
	left := rawESBatchSemanticQuery()
	left.TableID = "result_table.one"
	left.DB = "index-one"
	left.StorageID = "3"
	left.FieldAlias = metadata.FieldAlias{
		"alias_a": "field_a",
		"alias_b": "field_b",
	}

	right := rawESBatchSemanticQuery()
	right.TableID = "result_table.two"
	right.DB = "index-two"
	right.StorageID = "3"
	right.FieldAlias = metadata.FieldAlias{}
	right.FieldAlias["alias_b"] = "field_b"
	right.FieldAlias["alias_a"] = "field_a"

	leftFingerprint, err := rawESBatchSemanticFingerprint(left)
	require.NoError(t, err)
	rightFingerprint, err := rawESBatchSemanticFingerprint(right)
	require.NoError(t, err)

	assert.Equal(t, leftFingerprint, rightFingerprint)
}

func TestRawESBatchEligible(t *testing.T) {
	baseSettings := rawESBatchTestSettings()

	for _, testCase := range []struct {
		name     string
		settings queryRawESBatchSettings
		mutate   func(*metadata.Query)
		want     bool
	}{
		{name: "direct elasticsearch member", settings: baseSettings, want: true},
		{
			name:     "search after remains eligible",
			settings: baseSettings,
			mutate: func(query *metadata.Query) {
				query.ResultTableOption = &metadata.ResultTableOption{SearchAfter: []any{1}}
			},
			want: true,
		},
		{
			name:     "bkdata proxy",
			settings: baseSettings,
			mutate:   func(query *metadata.Query) { query.SourceType = structured.BkData },
			want:     false,
		},
		{
			name:     "scroll",
			settings: baseSettings,
			mutate:   func(query *metadata.Query) { query.Scroll = "1m" },
			want:     false,
		},
		{
			name:     "scroll id",
			settings: baseSettings,
			mutate: func(query *metadata.Query) {
				query.ResultTableOption = &metadata.ResultTableOption{ScrollID: "scroll-id"}
			},
			want: false,
		},
		{
			name:     "slice",
			settings: baseSettings,
			mutate: func(query *metadata.Query) {
				query.ResultTableOption = &metadata.ResultTableOption{SliceMax: 2}
			},
			want: false,
		},
		{
			name:     "non elasticsearch",
			settings: baseSettings,
			mutate:   func(query *metadata.Query) { query.StorageType = metadata.InfluxDBStorageType },
			want:     false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			query := rawESBatchSemanticQuery()
			query.StorageType = metadata.ElasticsearchStorageType
			query.StorageID = "3"
			if testCase.mutate != nil {
				testCase.mutate(query)
			}
			assert.Equal(t, testCase.want, rawESBatchEligible(testCase.settings, query))
		})
	}
}

func TestRawESBatchPlanGroupsByConnectionAndPreSemanticFingerprint(t *testing.T) {
	settings := rawESBatchTestSettings()
	connectionOne := rawESBatchTestConnectionKey(t, "connection-one")
	connectionTwo := rawESBatchTestConnectionKey(t, "connection-two")

	first := rawESBatchSemanticQuery()
	first.TableID = "result_table.one"
	first.DB = "index-one"
	second := rawESBatchSemanticQuery()
	second.TableID = "result_table.two"
	second.DB = "index-two"
	otherConnection := rawESBatchSemanticQuery()

	plan, err := planRawESBatch(settings, []rawESBatchPlanInput{
		rawESBatchTestPlanInput("a", 0, 0, connectionOne, first),
		rawESBatchTestPlanInput("a", 0, 1, connectionOne, second),
		rawESBatchTestPlanInput("b", 0, 0, connectionTwo, otherConnection),
	})
	require.NoError(t, err)
	require.Len(t, plan, 2)

	assert.Equal(t, rawESBatchExecutionCandidateGroup, plan[0].execution)
	require.Len(t, plan[0].members, 2)
	assert.Equal(t, []int{0, 1}, rawESBatchPlanOrdinals(plan[0]))

	assert.Equal(t, rawESBatchExecutionDirectSingle, plan[1].execution)
	require.Len(t, plan[1].members, 1)
	assert.Equal(t, 2, plan[1].members[0].ordinal)
}

func TestRawESBatchPlanSeparatesPreSemanticDifferences(t *testing.T) {
	connectionKey := rawESBatchTestConnectionKey(t, "shared-connection")

	testCases := []struct {
		name   string
		mutate func(*metadata.Query)
	}{
		{
			name: "all conditions",
			mutate: func(query *metadata.Query) {
				query.AllConditions[0][0].Value = []string{"trace-two"}
			},
		},
		{
			name:   "query string",
			mutate: func(query *metadata.Query) { query.QueryString = "trace_id:trace-two" },
		},
		{
			name: "time field",
			mutate: func(query *metadata.Query) {
				query.TimeField = metadata.TimeField{Name: "start_time", Type: "date", Unit: "millisecond"}
			},
		},
		{
			name: "sort",
			mutate: func(query *metadata.Query) {
				query.Orders = metadata.Orders{{Name: "trace_id", Ast: true}}
			},
		},
		{
			name:   "from",
			mutate: func(query *metadata.Query) { query.From++ },
		},
		{
			name:   "size",
			mutate: func(query *metadata.Query) { query.Size++ },
		},
		{
			name: "search after",
			mutate: func(query *metadata.Query) {
				query.ResultTableOption.SearchAfter = []any{1723679900001, "doc-4"}
			},
		},
		{
			name: "collapse",
			mutate: func(query *metadata.Query) {
				query.Collapse = &metadata.Collapse{Field: "span_id"}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			left := rawESBatchSemanticQuery()
			right := rawESBatchSemanticQuery()
			testCase.mutate(right)

			plan, err := planRawESBatch(rawESBatchTestSettings(), []rawESBatchPlanInput{
				rawESBatchTestPlanInput("a", 0, 0, connectionKey, left),
				rawESBatchTestPlanInput("a", 0, 1, connectionKey, right),
			})
			require.NoError(t, err)
			require.Len(t, plan, 2)
			assert.Equal(t, rawESBatchExecutionDirectSingle, plan[0].execution)
			assert.Equal(t, rawESBatchExecutionDirectSingle, plan[1].execution)
		})
	}
}

func TestRawESBatchPlanKeepsIneligibleMembersOnDirectPath(t *testing.T) {
	connectionKey := rawESBatchTestConnectionKey(t, "shared-connection")

	testCases := []struct {
		name     string
		settings queryRawESBatchSettings
		mutate   func(*metadata.Query)
	}{
		{
			name:     "bkdata",
			settings: rawESBatchTestSettings(),
			mutate:   func(query *metadata.Query) { query.SourceType = structured.BkData },
		},
		{
			name:     "scroll",
			settings: rawESBatchTestSettings(),
			mutate:   func(query *metadata.Query) { query.Scroll = "1m" },
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			left := rawESBatchSemanticQuery()
			right := rawESBatchSemanticQuery()
			if testCase.mutate != nil {
				testCase.mutate(left)
				testCase.mutate(right)
			}

			plan, err := planRawESBatch(testCase.settings, []rawESBatchPlanInput{
				rawESBatchTestPlanInput("a", 0, 0, connectionKey, left),
				rawESBatchTestPlanInput("a", 0, 1, connectionKey, right),
			})
			require.NoError(t, err)
			require.Len(t, plan, 2)
			assert.Equal(t, rawESBatchExecutionDirectSingle, plan[0].execution)
			assert.Equal(t, rawESBatchExecutionDirectSingle, plan[1].execution)
		})
	}
}

func TestRawESBatchPlanMarksSingletonExecutionStage(t *testing.T) {
	connectionKey := rawESBatchTestConnectionKey(t, "shared-connection")

	for _, testCase := range []struct {
		name      string
		prepared  bool
		execution rawESBatchExecution
	}{
		{name: "unprepared direct single", execution: rawESBatchExecutionDirectSingle},
		{name: "prepared single", prepared: true, execution: rawESBatchExecutionPreparedSingle},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			input := rawESBatchTestPlanInput(
				"a",
				0,
				0,
				connectionKey,
				rawESBatchSemanticQuery(),
			)
			input.prepared = testCase.prepared

			plan, err := planRawESBatch(rawESBatchTestSettings(), []rawESBatchPlanInput{input})
			require.NoError(t, err)
			require.Len(t, plan, 1)
			assert.Equal(t, testCase.execution, plan[0].execution)
		})
	}
}

func TestRawESBatchPlanIsStableAcrossInputMapOrder(t *testing.T) {
	connectionKey := rawESBatchTestConnectionKey(t, "shared-connection")
	queryA := rawESBatchSemanticQuery()
	queryA.TableID = "result_table.a"
	queryB := rawESBatchSemanticQuery()
	queryB.TableID = "result_table.b"
	queryC := rawESBatchSemanticQuery()
	queryC.TableID = "result_table.c"
	queryC.QueryString = "trace_id:trace-three"

	inputByReference := map[string][]rawESBatchPlanInput{
		"b": {rawESBatchTestPlanInput("b", 0, 0, connectionKey, queryB)},
		"a": {
			rawESBatchTestPlanInput("a", 0, 0, connectionKey, queryA),
			rawESBatchTestPlanInput("a", 0, 1, connectionKey, queryC),
		},
	}
	leftInput := append(
		append([]rawESBatchPlanInput(nil), inputByReference["b"]...),
		inputByReference["a"]...,
	)
	rightInput := append(
		append([]rawESBatchPlanInput(nil), inputByReference["a"]...),
		inputByReference["b"]...,
	)

	leftPlan, err := planRawESBatch(rawESBatchTestSettings(), leftInput)
	require.NoError(t, err)
	rightPlan, err := planRawESBatch(rawESBatchTestSettings(), rightInput)
	require.NoError(t, err)

	assert.Equal(t, rawESBatchPlanSummary(leftPlan), rawESBatchPlanSummary(rightPlan))
	assert.Equal(t, []string{"result_table.a", "result_table.b"}, rawESBatchPlanTableIDs(leftPlan[0]))
	assert.Equal(t, []int{0, 2}, rawESBatchPlanOrdinals(leftPlan[0]))
	assert.Equal(t, []string{"result_table.c"}, rawESBatchPlanTableIDs(leftPlan[1]))
	assert.Equal(t, []int{1}, rawESBatchPlanOrdinals(leftPlan[1]))
}

func TestRawESBatchPlanRejectsDuplicateStableLocation(t *testing.T) {
	connectionKey := rawESBatchTestConnectionKey(t, "shared-connection")
	_, err := planRawESBatch(rawESBatchTestSettings(), []rawESBatchPlanInput{
		rawESBatchTestPlanInput("a", 0, 0, connectionKey, rawESBatchSemanticQuery()),
		rawESBatchTestPlanInput("a", 0, 0, connectionKey, rawESBatchSemanticQuery()),
	})
	require.Error(t, err)
}

func rawESBatchTestSettings() queryRawESBatchSettings {
	return queryRawESBatchSettings{
		maxMembers:            DefaultQueryRawESBatchMaxMembers,
		maxBodyBytes:          DefaultQueryRawESBatchMaxBodyBytes,
		maxConcurrentSearches: DefaultQueryRawESBatchMaxConcurrentSearches,
	}
}

func rawESBatchTestConnectionKey(t *testing.T, address string) elasticsearch.RawBatchConnectionKey {
	t.Helper()
	ctx := context.Background()
	instance, err := elasticsearch.NewInstance(ctx, &elasticsearch.InstanceOption{
		Connect: elasticsearch.Connect{Address: address},
		Timeout: time.Second,
	})
	require.NoError(t, err)
	return instance.RawBatchConnectionKey(ctx)
}

func rawESBatchTestPlanInput(
	referenceName string,
	referenceIndex, queryIndex int,
	connectionKey elasticsearch.RawBatchConnectionKey,
	query *metadata.Query,
) rawESBatchPlanInput {
	if query.StorageType == "" {
		query.StorageType = metadata.ElasticsearchStorageType
	}
	if query.StorageID == "" {
		query.StorageID = "3"
	}
	return rawESBatchPlanInput{
		location: rawESBatchMemberLocation{
			referenceName:  referenceName,
			referenceIndex: referenceIndex,
			queryIndex:     queryIndex,
		},
		connectionKey: connectionKey,
		query:         query,
	}
}

func rawESBatchPlanOrdinals(group rawESBatchPlanGroup) []int {
	ordinals := make([]int, 0, len(group.members))
	for _, member := range group.members {
		ordinals = append(ordinals, member.ordinal)
	}
	return ordinals
}

func rawESBatchPlanTableIDs(group rawESBatchPlanGroup) []string {
	tableIDs := make([]string, 0, len(group.members))
	for _, member := range group.members {
		tableIDs = append(tableIDs, member.query.TableID)
	}
	return tableIDs
}

func rawESBatchPlanSummary(plan []rawESBatchPlanGroup) [][]any {
	summary := make([][]any, 0, len(plan))
	for _, group := range plan {
		summary = append(summary, []any{
			group.execution,
			rawESBatchPlanOrdinals(group),
			rawESBatchPlanTableIDs(group),
		})
	}
	return summary
}

func rawESBatchSemanticQuery() *metadata.Query {
	from := 3
	return &metadata.Query{
		SourceType:  "direct",
		DataSource:  "bk_apm",
		DB:          "trace-index",
		Field:       "trace_id",
		TimeField:   metadata.TimeField{Name: "min_start_time", Type: "long", Unit: "microsecond"},
		FieldAlias:  metadata.FieldAlias{"trace_id": "trace_id"},
		QueryString: "",
		AllConditions: metadata.AllConditions{{{
			DimensionName: "trace_id",
			Operator:      "eq",
			Value:         []string{"trace-one"},
		}}},
		Source: []string{"trace_id", "min_start_time"},
		From:   0,
		Size:   10,
		ResultTableOption: &metadata.ResultTableOption{
			From:        &from,
			SearchAfter: []any{1723679900000, "doc-3"},
		},
		Orders: metadata.Orders{{Name: "min_start_time", Ast: false}},
	}
}
