// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"encoding/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func (*qualityTestBackend) QueryPathResources(ctx context.Context, _, _, _ string, _ cmdb.Resource, _ []cmdb.Resource, _ [][]cmdb.Resource, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	if matcher["id"] == "partial" {
		metadata.SetStatus(ctx, metadata.QueryTsPartial, "backend_partial")
	}
	return []cmdb.PathResourcesResult{{Timestamp: 1700000000000, TargetType: "target", Path: []cmdb.PathNode{{ResourceType: "source", Dimensions: matcher}, {ResourceType: "target", Dimensions: cmdb.Matcher{"id": "result"}}}}}, nil
}
func (b *qualityTestBackend) QueryPathResourcesRange(ctx context.Context, a, biz, _, _, _ string, source cmdb.Resource, targets []cmdb.Resource, paths [][]cmdb.Resource, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	return b.QueryPathResources(ctx, a, biz, "", source, targets, paths, matcher)
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
		model.SetTimeGraphResolver(func(context.Context, string) (cmdb.CMDB, error) { return &qualityTestBackend{}, nil })
		t.Cleanup(func() {
			model.SetSchemaProvider(v1beta3.GetSchemaProvider())
			model.SetTimeGraphResolver(func(context.Context, string) (cmdb.CMDB, error) { return model, nil })
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
