// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type qualityTestSchema struct{}

func (qualityTestSchema) GetResourcePrimaryKeys(string, v1beta3.ResourceType) []string {
	return []string{"id"}
}

func (qualityTestSchema) GetResourceFields(string, v1beta3.ResourceType) []string {
	return []string{"id"}
}

func (qualityTestSchema) ListResourceTypes(string) []v1beta3.ResourceType {
	return []v1beta3.ResourceType{"source", "target"}
}

func (qualityTestSchema) ListRelationSchemas(string) []v1beta3.RelationSchema {
	return []v1beta3.RelationSchema{{FromType: "source", ToType: "target", RelationType: "source_to_target", Category: v1beta3.RelationCategoryStatic, IsDirectional: true}}
}

type qualityTestBackend struct{ cmdb.CMDB }

func (*qualityTestBackend) QueryRelationTimeGraph(ctx context.Context, _, _ string, _, _ time.Time, _ time.Duration, _ cmdb.Resource, _ []cmdb.Resource, _ []cmdb.RelationPath, matcher, _ cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	if strings.HasPrefix(matcher["id"], "partial") {
		reason := "backend_partial"
		if strings.Contains(matcher["id"], "coverage") {
			reason = "vm_storage_coverage_mismatch"
		}
		metadata.SetStatus(ctx, metadata.QueryTsPartial, reason)
	}
	if strings.HasSuffix(matcher["id"], "empty") {
		return nil, nil
	}
	return []cmdb.PathResourcesResult{{Timestamp: 1700000000000, TargetType: "target", Path: []cmdb.PathNode{{ResourceType: "source", Dimensions: matcher}, {ResourceType: "target", Dimensions: cmdb.Matcher{"id": "result"}}}}}, nil
}

func TestLegacyHTTPPartialDoesNotContaminateAnotherBatchItem(t *testing.T) {
	log.InitTestLogger()
	for _, rangeQuery := range []bool{false, true} {
		metadata.InitMetadata()
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, &metadata.User{SpaceUID: "space"})
		cmdbModel, err := v1beta3.GetModel(ctx)
		require.NoError(t, err)
		model := cmdbModel.(*v1beta3.Model)
		model.SetSchemaProvider(qualityTestSchema{})
		model.SetTimeGraphResolver(func(context.Context, string) (v1beta3.TimeGraphQuerier, error) { return &qualityTestBackend{}, nil })
		t.Cleanup(func() {
			model.SetSchemaProvider(v1beta3.GetSchemaProvider())
			model.SetTimeGraphResolver(func(context.Context, string) (v1beta3.TimeGraphQuerier, error) { return model, nil })
		})
		timeFields := `"timestamp":1700000000`
		if rangeQuery {
			timeFields = `"start_time":1700000000,"end_time":1700000060,"step":"60s"`
		}
		body := `{"query_list":[{` + timeFields + `,"source_type":"source","target_type":"target","source_info":{"id":"partial"}},{` + timeFields + `,"source_type":"source","target_type":"target","source_info":{"id":"complete"}}]}`
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body)).WithContext(ctx)
		if rangeQuery {
			HandlerAPIRelationV1Beta3MultiResourceRange(c)
		} else {
			HandlerAPIRelationV1Beta3MultiResource(c)
		}
		require.Equal(t, http.StatusOK, w.Code)
		var result struct {
			Data []map[string]json.RawMessage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		require.Len(t, result.Data, 2)
		require.Equal(t, "200", string(result.Data[0]["code"]))
		require.Equal(t, "true", string(result.Data[0]["partial"]))
		require.Equal(t, `"backend_partial"`, string(result.Data[0]["partial_reason"]))
		require.NotContains(t, result.Data[1], "partial")
		require.NotContains(t, result.Data[1], "partial_reason")
		require.Nil(t, metadata.GetStatus(ctx))
	}
}

// Assert the wire contract consumed by pages, alarm enrichment and relation caches.
func TestLegacyHTTPStorageCoverageQuality(t *testing.T) {
	log.InitTestLogger()
	for _, rangeQuery := range []bool{false, true} {
		for _, empty := range []bool{false, true} {
			t.Run(fmt.Sprintf("range=%t/empty=%t", rangeQuery, empty), func(t *testing.T) {
				metadata.InitMetadata()
				ctx := metadata.InitHashID(context.Background())
				metadata.SetUser(ctx, &metadata.User{SpaceUID: "space"})
				backend, err := v1beta3.GetModel(ctx)
				require.NoError(t, err)
				model := backend.(*v1beta3.Model)
				model.SetSchemaProvider(qualityTestSchema{})
				model.SetTimeGraphResolver(func(context.Context, string) (v1beta3.TimeGraphQuerier, error) { return &qualityTestBackend{}, nil })
				t.Cleanup(func() {
					model.SetSchemaProvider(v1beta3.GetSchemaProvider())
					model.SetTimeGraphResolver(func(context.Context, string) (v1beta3.TimeGraphQuerier, error) { return model, nil })
				})
				timeFields := `"timestamp":1700000000`
				if rangeQuery {
					timeFields = `"start_time":1700000000,"end_time":1700000060,"step":"60s"`
				}
				suffix := ""
				if empty {
					suffix = "-empty"
				}
				body := `{"query_list":[{` + timeFields + `,"source_type":"source","target_type":"target","source_info":{"id":"partial-coverage` + suffix + `"}},{` + timeFields + `,"source_type":"source","target_type":"target","source_info":{"id":"complete` + suffix + `"}}]}`
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body)).WithContext(ctx)
				if rangeQuery {
					HandlerAPIRelationV1Beta3MultiResourceRange(c)
				} else {
					HandlerAPIRelationV1Beta3MultiResource(c)
				}
				require.Equal(t, http.StatusOK, w.Code)
				var response struct {
					Data []map[string]any `json:"data"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
				require.Len(t, response.Data, 2)
				require.Equal(t, true, response.Data[0]["partial"])
				require.Equal(t, "vm_storage_coverage_mismatch", response.Data[0]["partial_reason"])
				require.NotContains(t, response.Data[1], "partial")
				require.NotContains(t, response.Data[1], "partial_reason")
				for _, item := range response.Data {
					require.Equal(t, float64(200), item["code"])
					if empty {
						require.Empty(t, item["target_list"])
					} else {
						require.NotEmpty(t, item["target_list"])
					}
				}
				// Synthetic identities only; this log can be replayed by consumer contract tests.
				t.Logf("RELATION_COVERAGE_HTTP %s", w.Body.String())
			})
		}
	}
}
