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
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

const (
	sqlProxyURLEnv = "UQ_V1BETA3_SQL_PROXY_URL"
	sqlProxyNSEnv  = "UQ_V1BETA3_SQL_PROXY_NS"
	sqlProxyDBEnv  = "UQ_V1BETA3_SQL_PROXY_DB"
)

type sqlProxyStatementResponse struct {
	Status string          `json:"status"`
	Detail string          `json:"detail"`
	Result json.RawMessage `json:"result"`
}

type sqlProxyIntegrationResponse struct {
	Source  cmdb.Resource                `json:"source"`
	Matcher cmdb.Matcher                 `json:"matcher"`
	Path    []string                     `json:"path"`
	Target  cmdb.Resource                `json:"target"`
	Series  []cmdb.MatchersWithTimestamp `json:"series"`
}

func TestSurrealQLSQLProxyIntegration(t *testing.T) {
	proxyURL := os.Getenv(sqlProxyURLEnv)
	if proxyURL == "" {
		t.Skipf("set %s=http://127.0.0.1:18000/sql to run local BKBase /sql proxy integration test", sqlProxyURLEnv)
	}

	proxyNS := envOrDefault(sqlProxyNSEnv, "mapleleaf_2")
	proxyDB := envOrDefault(sqlProxyDBEnv, "2_bkm_10_bkcc_built_in_time_series_graph")
	provider := sqlProxyHostModuleProvider()

	tests := []struct {
		name             string
		req              *QueryRequest
		stepMs           int64
		wantSQL          string
		wantResponseJSON string
	}{
		{
			name: "host to module by local BKBase SQL proxy",
			req: &QueryRequest{
				Timestamp:          1782558879000,
				LookBackDelta:      2000,
				SourceType:         ResourceTypeHost,
				SourceInfo:         map[string]string{"bk_host_id": "77591"},
				TargetType:         ResourceTypeModule,
				TargetTypeExplicit: true,
				MaxHops:            1,
				Limit:              1,
			},
			stepMs: 2000,
			wantSQL: `LET $timestamp = 1782558879000;
LET $look_back_delta = 2000;
LET $start = 1782558877;
LET $end = 1782558879;
LET $start_ms = 1782558877000;
LET $end_ms = 1782558879000;

SELECT {
    root: {
        entity_type: meta::tb(id),
        entity_id: <string>id,
        entity_data: { bk_host_id: bk_host_id },
        created_at: created_at,
        updated_at: updated_at,
        liveness: (SELECT * FROM host_liveness_record WHERE reference_id = $parent.id AND period_end >= $start AND period_start <= $end)
    },

    hop1: {
        host_with_module: (SELECT VALUE {
            hop: 1,
            relation_type: 'host_with_module',
            relation_category: 'static',
            relation_id: <string>id,
            relation_liveness: (SELECT * FROM host_with_module_liveness_record WHERE relation_id = $parent.id AND period_end >= $start_ms AND period_start <= $end_ms),
            target: {
                entity_type: 'module',
                entity_id: <string>out,
                entity_data: { bk_module_id: out.bk_module_id },
                liveness: (SELECT * FROM module_liveness_record WHERE reference_id = $parent.out AND period_end >= $start AND period_start <= $end)
            }
        } FROM host_with_module WHERE in = $parent.id
          AND (SELECT * FROM host_with_module_liveness_record WHERE relation_id = $parent.id AND $end_ms >= period_start AND $start_ms <= period_end LIMIT 1)[0] != NONE
          AND (SELECT * FROM module_liveness_record WHERE reference_id = $parent.out AND $end >= period_start AND $start <= period_end LIMIT 1)[0] != NONE
          LIMIT 1001)
    }
} AS result
FROM host
WHERE bk_host_id = '77591'
  AND (SELECT * FROM host_liveness_record WHERE reference_id = $parent.id AND $end >= period_start AND $start <= period_end LIMIT 1)[0] != NONE
LIMIT 1;`,
			wantResponseJSON: `{
  "source": "host",
  "matcher": {
    "bk_host_id": "77591"
  },
  "path": [
    "host",
    "module"
  ],
  "target": "module",
  "series": [
    {
      "timestamp": 1782558879000,
      "items": [
        {
          "bk_module_id": "6651"
        }
      ]
    }
  ]
}`,
		},
		{
			name: "explicit host to host without self relation by local BKBase SQL proxy",
			req: &QueryRequest{
				Timestamp:          1782558879000,
				LookBackDelta:      2000,
				SourceType:         ResourceTypeHost,
				SourceInfo:         map[string]string{"bk_host_id": "77591"},
				TargetType:         ResourceTypeHost,
				TargetTypeExplicit: true,
				MaxHops:            1,
				Limit:              1,
			},
			stepMs: 2000,
			wantSQL: `LET $timestamp = 1782558879000;
LET $look_back_delta = 2000;
LET $start = 1782558877;
LET $end = 1782558879;
LET $start_ms = 1782558877000;
LET $end_ms = 1782558879000;

SELECT {
    root: {
        entity_type: meta::tb(id),
        entity_id: <string>id,
        entity_data: { bk_host_id: bk_host_id },
        created_at: created_at,
        updated_at: updated_at,
        liveness: (SELECT * FROM host_liveness_record WHERE reference_id = $parent.id AND period_end >= $start AND period_start <= $end)
    },

    hop1: {}
} AS result
FROM host
WHERE bk_host_id = '77591'
  AND (SELECT * FROM host_liveness_record WHERE reference_id = $parent.id AND $end >= period_start AND $start <= period_end LIMIT 1)[0] != NONE
LIMIT 1;`,
			wantResponseJSON: `{
  "source": "host",
  "matcher": {
    "bk_host_id": "77591"
  },
  "path": [
    "host"
  ],
  "target": "host",
  "series": null
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql := NewSurrealQueryBuilderWithSchemaProvider(tt.req, provider).Build()
			assert.Equal(t, tt.wantSQL, sql)

			// 本地 /sql 代理负责补齐 USE NS/DB，这里把 UQ 生成的 SurrealQL 原样送进去。
			rawResponse := executeLocalSQLProxy(t, proxyURL, proxyNS, proxyDB, sql)
			queryStart, queryEnd := tt.req.GetQueryRange()
			graphs, err := NewSurrealResponseParser(queryStart, queryEnd).Parse(rawResponse)
			require.NoError(t, err)
			require.NotEmpty(t, graphs)
			for _, graph := range graphs {
				require.Empty(t, graph.TraversalErrors)
			}

			pathFinder := NewPathFinder(
				WithAllowedCategories(tt.req.AllowedRelationTypes...),
				WithDynamicDirection(tt.req.DynamicRelationDirection),
				WithMaxHops(tt.req.MaxHops),
				WithSchemaProvider(provider),
				WithNamespace(tt.req.SchemaNamespace()),
			)
			paths, err := pathFinder.FindAllPaths(tt.req.SourceType, tt.req.TargetType, tt.req.PathResource)
			require.NoError(t, err)
			responsePath := resourceTypesToPath(
				resourcePathForRangeQuery(graphs, paths, tt.req, queryStart, queryEnd, tt.stepMs),
			)

			gotResponse := sqlProxyIntegrationResponse{
				Source:  cmdb.Resource(tt.req.SourceType),
				Matcher: cmdb.Matcher(tt.req.SourceInfo),
				Path:    responsePath,
				Target:  cmdb.Resource(tt.req.TargetType),
				Series: buildTargetMatchersTimeSeriesWithOptions(
					graphs,
					tt.req.TargetType,
					targetExtractionPathResource(tt.req),
					queryStart,
					queryEnd,
					tt.stepMs,
					provider,
					tt.req.SchemaNamespace(),
					tt.req.TargetInfoShow,
					shouldIncludeRootTarget(tt.req),
				),
			}
			assert.Equal(t, mustParseSQLProxyIntegrationResponse(t, tt.wantResponseJSON), gotResponse)
		})
	}
}

func mustParseSQLProxyIntegrationResponse(t *testing.T, raw string) sqlProxyIntegrationResponse {
	t.Helper()

	var response sqlProxyIntegrationResponse
	require.NoError(t, json.Unmarshal([]byte(raw), &response))
	return response
}

func executeLocalSQLProxy(t *testing.T, proxyURL, ns, db, sql string) []map[string]any {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, proxyURL, bytes.NewBufferString(sql))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("ns", ns)
	req.Header.Set("db", db)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))

	var statements []sqlProxyStatementResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	require.NoError(t, decoder.Decode(&statements), string(body))
	require.NotEmpty(t, statements)

	statement := statements[0]
	require.Equal(t, "OK", statement.Status, "proxy detail: %s", statement.Detail)

	var result []any
	decoder = json.NewDecoder(bytes.NewReader(statement.Result))
	decoder.UseNumber()
	require.NoError(t, decoder.Decode(&result), string(statement.Result))

	// 与生产 BKBaseSurrealDBClient 保持一致：/sql 的 result 就是 data.list，需要包回 parser 的 rawResponse 壳。
	return []map[string]any{
		{ResponseFieldResult: result},
	}
}

func sqlProxyHostModuleProvider() SchemaProvider {
	return NewSchemaProviderFromRelation(relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourceDefinitions: []*relation.ResourceDefinition{
			{
				Name: string(ResourceTypeHost),
				Fields: []relation.FieldDefinition{
					{Name: "bk_host_id", Required: true},
					{Name: "created_at"},
					{Name: "updated_at"},
				},
			},
			{
				Name: string(ResourceTypeModule),
				Fields: []relation.FieldDefinition{
					{Name: "bk_module_id", Required: true},
					{Name: "created_at"},
					{Name: "updated_at"},
				},
			},
		},
		RelationSchemas: []relation.RelationSchema{
			{
				RelationName: "host_with_module",
				Category:     relation.RelationCategoryStatic,
				FromType:     relation.ResourceType(ResourceTypeHost),
				ToType:       relation.ResourceType(ResourceTypeModule),
			},
		},
	}))
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
