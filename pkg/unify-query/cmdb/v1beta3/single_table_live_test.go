// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	traceservice "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/trace"
	uqtrace "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// This opt-in, read-only acceptance test runs inside the query Pod. Credentials
// arrive on stdin and are never logged or stored by the test.
type singleTableLiveConfig struct {
	Host, User, Password, Namespace, Database string
	Trace                                     map[string]any
	Tables                                    []struct{ Table, Source, Target string }
}

type singleTableLiveExecutor struct{ config singleTableLiveConfig }

func (e *singleTableLiveExecutor) query(ctx context.Context, sql string) ([]map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.config.Host+"/sql", strings.NewReader(sql))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("surreal-ns", e.config.Namespace)
	req.Header.Set("surreal-db", e.config.Database)
	req.SetBasicAuth(e.config.User, e.config.Password)
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("database transport failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("database HTTP status %d", res.StatusCode)
	}
	var statements []map[string]any
	if err := json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&statements); err != nil {
		return nil, err
	}
	for i, s := range statements {
		if s["status"] != "OK" {
			return nil, fmt.Errorf("statement %d: %v", i, s["result"])
		}
	}
	return statements, nil
}

func (e *singleTableLiveExecutor) Execute(ctx context.Context, sql string, start, end int64) (graphs []*LivenessGraph, err error) {
	ctx, span := uqtrace.NewSpan(ctx, "surrealdb-single-table-direct")
	defer span.End(&err)
	span.Set("dsl", sql)
	span.Set("namespace", e.config.Namespace)
	span.Set("database", e.config.Database)
	statements, err := e.query(ctx, sql)
	if err != nil {
		return nil, err
	}
	rows, err := normalizeDirectSurrealDBResponse(statements)
	if err != nil {
		return nil, err
	}
	return NewSurrealResponseParser(start, end).Parse([]map[string]any{{"result": rows}})
}

func liveRows(t *testing.T, e *singleTableLiveExecutor, sql string) []any {
	t.Helper()
	s, err := e.query(context.Background(), sql)
	require.NoError(t, err)
	require.NotEmpty(t, s)
	rows, ok := responseArray(s[len(s)-1]["result"])
	require.True(t, ok)
	return rows
}

func liveMatcher(t *testing.T, provider SchemaProvider, resource ResourceType, row map[string]any) map[string]string {
	t.Helper()
	result := map[string]string{}
	keys := provider.GetResourcePrimaryKeys("", resource)
	require.NotEmpty(t, keys)
	for _, key := range keys {
		value, ok := row[key].(string)
		require.True(t, ok, "missing entity key %s for %s", key, resource)
		result[key] = value
	}
	return result
}

func liveTargetIDs(graphs []*LivenessGraph, relation RelationType) []string {
	ids := map[string]bool{}
	for _, g := range graphs {
		for _, edge := range g.Edges {
			if edge.RelationType == relation {
				ids[edge.ToID] = true
			}
		}
	}
	result := []string{}
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func liveReferenceIDs(rows []any, start, end int64) []string {
	ids := map[string]bool{}
	for _, v := range rows {
		r := v.(map[string]any)
		a := int64(r["start"].(float64))
		b := int64(r["end"].(float64))
		if a <= b && a <= end && b >= start {
			ids[r["target"].(string)] = true
		}
	}
	result := []string{}
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func TestSingleTableLiveAcceptance(t *testing.T) {
	if os.Getenv("UQ_SINGLE_TABLE_LIVE") != "1" {
		t.Skip("requires explicit read-only database configuration on stdin")
	}
	var config singleTableLiveConfig
	require.NoError(t, json.NewDecoder(os.Stdin).Decode(&config))
	require.NotEmpty(t, config.Tables)
	executor := &singleTableLiveExecutor{config: config}
	if len(config.Trace) > 0 {
		viper.Set("trace", config.Trace)
		viper.Set("trace.enable", true)
		viper.Set("trace.service_name", "unify-query-single-table-test")
		traceservice.InitConfig()
		service := &traceservice.Service{}
		service.Start(context.Background())
		defer func() { service.Close(); service.Wait() }()
	}
	provider := NewStaticSchemaProvider()
	populated := 0
	for _, table := range config.Tables {
		t.Run(table.Table, func(t *testing.T) {
			var sampleRows []any
			for _, offset := range []int{0, 128, 512, 2048} {
				rows := liveRows(t, executor, fmt.Sprintf("SELECT source_id, target_id, source_id.* AS source_entity, target_id.* AS target_entity, active_period_start_ms, active_period_end_ms FROM %s LIMIT 1 START %d;", surrealTableName(table.Table), offset))
				if len(rows) == 0 {
					break
				}
				row := rows[0].(map[string]any)
				valid := true
				for _, endpoint := range []struct{ field, resource string }{{"source_entity", table.Source}, {"target_entity", table.Target}} {
					entity, _ := row[endpoint.field].(map[string]any)
					for _, key := range provider.GetResourcePrimaryKeys("", ResourceType(endpoint.resource)) {
						value, ok := entity[key].(string)
						if !ok || value == "" {
							valid = false
						}
					}
				}
				if valid {
					sampleRows = rows
					break
				}
			}
			if len(sampleRows) == 0 {
				t.Skip("no positive sample with complete endpoint keys within bounded sampling")
			}
			populated++
			sample := sampleRows[0].(map[string]any)
			end := int64(sample["active_period_end_ms"].(float64))
			start := int64(sample["active_period_start_ms"].(float64))
			if end > start+300000 {
				end = start + 300000
			}
			for _, reverse := range []bool{false, true} {
				sourceType, targetType := ResourceType(table.Source), ResourceType(table.Target)
				sourceField, targetField, entityField := "source_id", "target_id", "source_entity"
				direction := DirectionOutbound
				if reverse {
					sourceType, targetType = targetType, sourceType
					sourceField, targetField, entityField = "target_id", "source_id", "target_entity"
					direction = DirectionInbound
				}
				sourceID := sample[sourceField].(string)
				entity := sample[entityField].(map[string]any)
				sourceInfo := liveMatcher(t, provider, sourceType, entity)
				relationType := RelationType(table.Table)
				request := &QueryRequest{Timestamp: end, LookBackDelta: end - start, SourceType: sourceType, SourceInfo: sourceInfo, TargetType: targetType, TargetTypeExplicit: true, MaxHops: 1}
				path := resourcePath{Steps: []resourcePathStep{{ResourceType: string(sourceType)}, {ResourceType: string(targetType), RelationType: table.Table, Category: string(RelationCategoryStatic), Direction: string(direction)}}}
				// Use the live table contract, independent of optional global relation categories.
				resources := map[ResourceType]tableResourceDefinition{}
				for _, r := range []ResourceType{sourceType, targetType} {
					resources[r] = tableResourceDefinition{primaryKeys: provider.GetResourcePrimaryKeys("", r)}
				}
				schema := newTableSchemaProvider(resources, []RelationSchema{{RelationType: relationType, Category: RelationCategoryStatic, FromType: ResourceType(table.Source), ToType: ResourceType(table.Target)}})
				reference := liveRows(t, executor, fmt.Sprintf("SELECT <string>%s AS target, active_period_start_ms AS start, active_period_end_ms AS end FROM %s WHERE %s = <record>'%s';", targetField, surrealTableName(table.Table), sourceField, escapeSurrealString(sourceID)))
				for _, mode := range []graphQueryMode{graphQueryModeInstant, graphQueryModeRange} {
					t.Run(fmt.Sprintf("%s/%s", direction, mode), func(t *testing.T) {
						builder := NewSurrealQueryBuilderForPath(request, schema, path)
						configureBuilderForGraphQueryMode(builder, mode)
						sql := builder.Build()
						require.NotContains(t, sql, "_liveness_record")
						require.NotContains(t, sql, "_active_edge_view")
						require.NotContains(t, sql, "source_data")
						require.NotContains(t, sql, "target_data")
						graphs, err := executor.Execute(context.Background(), sql, start, end)
						require.NoError(t, err)
						require.Equal(t, liveReferenceIDs(reference, start, end), liveTargetIDs(graphs, relationType))
						require.NotEmpty(t, graphs, "positive sample must produce results")
						model, err := NewModel(context.Background(), executor)
						require.NoError(t, err)
						model.SetSchemaProvider(schema)
						ctx, span := uqtrace.NewSpan(context.Background(), "single-table-acceptance")
						span.Set("test-case", table.Table+"/"+string(direction)+"/"+string(mode))
						span.Set("test-request", request)
						modelGraphs, modelPaths, modelMatchers, err := model.queryLivenessGraph(ctx, cloneQueryRequest(request), mode, start, end, 60000)
						span.Set("target-count", len(liveTargetIDs(modelGraphs, relationType)))
						span.End(&err)
						response := map[string]any{"paths": modelPaths, "matchers": modelMatchers}
						defer func() {
							record, _ := json.Marshal(map[string]any{"case": table.Table, "direction": direction, "mode": mode, "trace_id": span.TraceID(), "request": request, "response": response, "target_count": len(liveTargetIDs(modelGraphs, relationType))})
							t.Logf("TRACE_RECORD %s", record)
						}()
						require.NoError(t, err)
						require.Equal(t, liveReferenceIDs(reference, start, end), liveTargetIDs(modelGraphs, relationType), "Model result must match independent table read")
						for _, g := range graphs {
							for _, edge := range g.Edges {
								labels := g.GetNode(edge.ToID).Labels
								for _, key := range schema.GetResourcePrimaryKeys("", targetType) {
									_, ok := labels[key]
									require.True(t, ok, "missing target label %s", key)
								}
							}
						}
						if mode == graphQueryModeRange {
							for _, g := range graphs {
								for _, edge := range g.Edges {
									require.NotEmpty(t, edge.RawPeriods)
								}
							}
							points := buildTargetMatchersTimeSeriesWithOptions(modelGraphs, targetType, []ResourceType{sourceType, targetType}, start, end, 60000, schema, "", false, false)
							response["range_result"] = points
							byTime := map[int64]cmdb.Matchers{}
							for _, point := range points {
								byTime[point.Timestamp] = point.Matchers
							}
							for bucket := start; bucket <= end; bucket += 60000 {
								// The public range contract is count_over_time over (bucket-step, bucket].
								expected := map[string]bool{}
								for _, value := range reference {
									row := value.(map[string]any)
									a, b := int64(row["start"].(float64)), int64(row["end"].(float64))
									if a <= b && a <= end && b >= start && a <= bucket && b > bucket-60000 {
										expected[row["target"].(string)] = true
									}
								}
								require.Len(t, byTime[bucket], len(expected), "bucket %d", bucket)
							}
						}
					})
				}
			}
		})
	}
	t.Logf("relations=%d positive_samples=%d empty_tables=%d", len(config.Tables), populated, len(config.Tables)-populated)
	t.Run("host_module_set", func(t *testing.T) { liveHostModuleSet(t, executor, provider) })
}

func liveHostModuleSet(t *testing.T, executor *singleTableLiveExecutor, provider SchemaProvider) {
	rows := liveRows(t, executor, "SELECT source_id, source_id.* AS entity, last_seen_at FROM host_with_module LIMIT 1;")
	require.NotEmpty(t, rows)
	sample := rows[0].(map[string]any)
	end := int64(sample["last_seen_at"].(float64))
	start := end - 1
	sourceID := sample["source_id"].(string)
	referenceSQL := fmt.Sprintf(`LET $modules = (SELECT VALUE target_id FROM host_with_module WHERE source_id = <record>'%s' AND active_period_start_ms <= %d AND active_period_end_ms >= %d AND active_period_start_ms <= active_period_end_ms);
SELECT <string>target_id AS target, active_period_start_ms AS start, active_period_end_ms AS end FROM module_with_set WHERE source_id IN $modules AND active_period_start_ms <= %d AND active_period_end_ms >= %d AND active_period_start_ms <= active_period_end_ms;`, escapeSurrealString(sourceID), end, start, end, start)
	reference := liveRows(t, executor, referenceSQL)
	require.NotEmpty(t, reference, "two-hop positive reference required")
	resources := map[ResourceType]tableResourceDefinition{}
	for _, r := range []ResourceType{ResourceTypeHost, ResourceTypeModule, ResourceTypeSet} {
		resources[r] = tableResourceDefinition{primaryKeys: provider.GetResourcePrimaryKeys("", r)}
	}
	schema := newTableSchemaProvider(resources, []RelationSchema{
		{RelationType: RelationHostWithModule, Category: RelationCategoryStatic, FromType: ResourceTypeHost, ToType: ResourceTypeModule},
		{RelationType: RelationModuleWithSet, Category: RelationCategoryStatic, FromType: ResourceTypeModule, ToType: ResourceTypeSet},
	})
	request := &QueryRequest{Timestamp: end, LookBackDelta: 1, LookBackDeltaSet: true, SourceType: ResourceTypeHost, SourceInfo: liveMatcher(t, provider, ResourceTypeHost, sample["entity"].(map[string]any)), TargetType: ResourceTypeSet, TargetTypeExplicit: true, MaxHops: 2, PathResource: []ResourceType{ResourceTypeModule}}
	model, err := NewModel(context.Background(), executor)
	require.NoError(t, err)
	model.SetSchemaProvider(schema)
	for _, mode := range []graphQueryMode{graphQueryModeInstant, graphQueryModeRange} {
		t.Run(string(mode), func(t *testing.T) {
			ctx, span := uqtrace.NewSpan(context.Background(), "single-table-acceptance-multi-hop")
			span.Set("test-case", "host/module/set/"+string(mode))
			span.Set("test-request", request)
			graphs, paths, matchers, err := model.queryLivenessGraph(ctx, cloneQueryRequest(request), mode, start, end, 1)
			span.Set("target-count", len(matchers))
			span.End(&err)
			record, _ := json.Marshal(map[string]any{"case": "host/module/set", "mode": mode, "trace_id": span.TraceID(), "request": request, "response": map[string]any{"paths": paths, "matchers": matchers}, "target_count": len(matchers)})
			t.Logf("TRACE_RECORD %s", record)
			require.NoError(t, err)
			require.NotEmpty(t, matchers)
			require.Len(t, paths, 1)
			require.Len(t, paths[0].Steps, 3)
			require.Equal(t, liveReferenceIDs(reference, start, end), liveTargetIDs(graphs, RelationModuleWithSet))
		})
	}
}
