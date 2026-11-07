// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

// The legacy handler is the oracle in these tests: both URLs get the same request
// in the same environment, with the router initialized and no route for the space.
func useRelationParityEnvironment(t *testing.T) {
	t.Helper()
	mock.Init()
	promql.MockEngine()
	influxdb.MockSpaceRouter(metadata.InitHashID(context.Background()))
	useRelationTestConfig(t)
	log.InitTestLogger()
	metadata.InitMetadata()
	previous := v1beta3.GetUnderlyingProvider(v1beta3.GetSchemaProvider())
	v1beta3.InitSchemaProvider(nil)
	v1beta1.InitSchemaProvider(relation.NewDefaultStaticSchemaProvider())
	t.Cleanup(func() { v1beta3.InitSchemaProvider(previous) })
}

var relationParityEntries = []struct {
	name          string
	timeFields    string
	messagePrefix string
	legacy, v3    gin.HandlerFunc
}{
	{
		name: "instant", timeFields: `"timestamp":1700000000`,
		legacy: HandlerAPIRelationMultiResource, v3: HandlerAPIRelationV1Beta3MultiResource,
	},
	{
		name: "range", timeFields: `"start_time":1700000000,"end_time":1700000060,"step":"60s"`,
		messagePrefix: "关联数据查询异常: ",
		legacy:        HandlerAPIRelationMultiResourceRange, v3: HandlerAPIRelationV1Beta3MultiResourceRange,
	},
}

func relationParityItem(t *testing.T, handler gin.HandlerFunc, space, query string) map[string]any {
	t.Helper()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: space})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"query_list":[`+query+`]}`)).WithContext(ctx)
	handler(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Len(t, response.Data, 1)
	return response.Data[0]
}

func TestRelationCompatEmptySpace(t *testing.T) {
	useRelationParityEnvironment(t)
	const spaceError = "space uid is empty"
	for _, entry := range relationParityEntries {
		for _, tc := range []struct {
			name, fields, wantMessage string
		}{
			{"explicit source", `"source_type":"pod","target_type":"node","source_info":{"bcs_cluster_id":"c","namespace":"ns","pod":"p"}`, spaceError},
			{"inferred source", `"target_type":"system","path_resource":["node"],"source_info":{"bcs_cluster_id":"c","namespace":"ns","pod":"p"}`, spaceError},
			{"with expand", `"source_type":"container","target_type":"pod","source_info":{"bcs_cluster_id":"c","namespace":"ns","pod":"p","container":"x"},"source_expand_info":{"version":"1.0"}`, spaceError},
			// The space is checked before the source type is looked up in the schema.
			{"unknown source", `"source_type":"unknown","source_info":{"id":"x"}`, spaceError},
			// Inference runs first, so its failure wins over the missing space.
			{"failed inference", `"target_type":"system","source_info":{"device":"x"}`, "get resource error: resource is empty with map[device:x]"},
		} {
			t.Run(entry.name+"/"+tc.name, func(t *testing.T) {
				query := "{" + entry.timeFields + "," + tc.fields + "}"
				want := relationParityItem(t, entry.legacy, "", query)
				require.Equal(t, float64(http.StatusBadRequest), want["code"])
				require.Equal(t, entry.messagePrefix+tc.wantMessage, want["message"])
				require.Equal(t, want, relationParityItem(t, entry.v3, "", query))
			})
		}
	}
}

func TestRelationCompatSpaceCheckOnlyForEmptySpace(t *testing.T) {
	useRelationParityEnvironment(t)
	for _, entry := range relationParityEntries {
		t.Run(entry.name, func(t *testing.T) {
			item := relationParityItem(t, entry.v3, "relation-parity-no-route",
				"{"+entry.timeFields+`,"source_type":"pod","target_type":"node","source_info":{"bcs_cluster_id":"c","namespace":"ns","pod":"p"}}`)
			require.Equal(t, float64(http.StatusOK), item["code"], item["message"])
			require.NotContains(t, item["message"], "space uid is empty")
		})
	}
}

func TestRelationCompatSourceInfoHoldsOnlyPrimaryKeys(t *testing.T) {
	useRelationParityEnvironment(t)
	for _, entry := range relationParityEntries {
		for _, tc := range []struct {
			name, fields string
			want         map[string]any
		}{
			{
				name:   "expand attribute",
				fields: `"source_type":"container","target_type":"pod","source_info":{"bcs_cluster_id":"c","namespace":"ns","pod":"p","container":"x"},"source_expand_info":{"version":"1.0"}`,
				want:   map[string]any{"bcs_cluster_id": "c", "namespace": "ns", "pod": "p", "container": "x"},
			},
			{
				name:   "expand holds a primary key",
				fields: `"source_type":"pod","target_type":"node","source_info":{"pod":"p"},"source_expand_info":{"namespace":"ns"}`,
				want:   map[string]any{"pod": "p"},
			},
			{
				name:   "expand holds every primary key",
				fields: `"source_type":"pod","target_type":"node","source_expand_info":{"bcs_cluster_id":"c","namespace":"ns","pod":"p"}`,
				want:   map[string]any{},
			},
			{
				name:   "field outside the primary key",
				fields: `"source_type":"pod","target_type":"node","source_info":{"bcs_cluster_id":"c","namespace":"ns","pod":"p","ignored":"v"}`,
				want:   map[string]any{"bcs_cluster_id": "c", "namespace": "ns", "pod": "p"},
			},
			{
				name:   "inferred source",
				fields: `"target_type":"system","path_resource":["node"],"source_info":{"bcs_cluster_id":"c","namespace":"ns","pod":"p"}`,
				want:   map[string]any{"bcs_cluster_id": "c", "namespace": "ns", "pod": "p"},
			},
		} {
			t.Run(entry.name+"/"+tc.name, func(t *testing.T) {
				query := "{" + entry.timeFields + "," + tc.fields + "}"
				legacy := relationParityItem(t, entry.legacy, "relation-parity-no-route", query)
				require.Equal(t, float64(http.StatusOK), legacy["code"], legacy["message"])
				require.Equal(t, tc.want, legacy["source_info"])

				got := relationParityItem(t, entry.v3, "relation-parity-no-route", query)
				require.Equal(t, float64(http.StatusOK), got["code"], got["message"])
				require.Equal(t, legacy["source_info"], got["source_info"])
				require.Equal(t, legacy["source_type"], got["source_type"])
				require.Equal(t, legacy["target_type"], got["target_type"])
				require.Equal(t, legacy["target_list"], got["target_list"])
			})
		}
	}
}

func TestRelationCompatInferenceReadsOnlySourceInfo(t *testing.T) {
	useRelationParityEnvironment(t)
	for _, entry := range relationParityEntries {
		for _, tc := range []struct {
			name, fields, wantType string
			wantCode               int
		}{
			{
				name:     "expand completes a longer primary key",
				fields:   `"target_type":"system","source_info":{"bk_host_id":"1","app_name":"a"},"source_expand_info":{"version":"v1"}`,
				wantType: "host", wantCode: http.StatusOK,
			},
			{
				name:     "expand completes the only primary key",
				fields:   `"target_type":"system","source_info":{"app_name":"a"},"source_expand_info":{"version":"v1"}`,
				wantCode: http.StatusBadRequest,
			},
			{
				name:     "expand holds the whole primary key",
				fields:   `"target_type":"system","source_expand_info":{"bk_host_id":"1"}`,
				wantCode: http.StatusBadRequest,
			},
			// The longer key still wins when source_info holds it.
			{
				name:     "source_info holds the longer primary key",
				fields:   `"target_type":"system","source_info":{"bk_host_id":"1","app_name":"a","version":"v1"}`,
				wantType: "app_version", wantCode: http.StatusOK,
			},
			// node sorts first, so this one tells the longest key from the first match.
			{
				name:     "longer primary key sorts after a shorter one",
				fields:   `"target_type":"system","source_info":{"bcs_cluster_id":"c","node":"n","namespace":"ns","pod":"p"}`,
				wantType: "pod", wantCode: http.StatusOK,
			},
		} {
			t.Run(entry.name+"/"+tc.name, func(t *testing.T) {
				query := "{" + entry.timeFields + "," + tc.fields + "}"
				legacy := relationParityItem(t, entry.legacy, "relation-parity-no-route", query)
				require.Equal(t, float64(tc.wantCode), legacy["code"], legacy["message"])
				require.Equal(t, tc.wantType, legacy["source_type"])
				require.Equal(t, legacy, relationParityItem(t, entry.v3, "relation-parity-no-route", query))
			})
		}
	}
}

func TestRelationCompatExpandConflictOnlyOnPrimaryKeys(t *testing.T) {
	useRelationParityEnvironment(t)
	const primaryKeys = `"bcs_cluster_id":"c","namespace":"ns","pod":"p","container":"x"`
	for _, entry := range relationParityEntries {
		for _, source := range []string{`"source_type":"container",`, ""} {
			conflict := "{" + entry.timeFields + "," + source + `"target_type":"pod","source_info":{` + primaryKeys + `},"source_expand_info":{"pod":"p2"}}`
			attribute := "{" + entry.timeFields + "," + source + `"target_type":"pod","source_info":{` + primaryKeys + `,"version":"v1"},"source_expand_info":{"version":"v2"}}`

			t.Run(entry.name+"/primary key/"+source, func(t *testing.T) {
				legacy := relationParityItem(t, entry.legacy, "relation-parity-no-route", conflict)
				require.Equal(t, float64(http.StatusOK), legacy["code"], legacy["message"])
				require.Empty(t, legacy["target_list"])
				got := relationParityItem(t, entry.v3, "relation-parity-no-route", conflict)
				require.Equal(t, float64(http.StatusBadRequest), got["code"])
				require.Contains(t, got["message"], `source_expand_info conflicts with source_info for field "pod"`)
			})
			t.Run(entry.name+"/attribute/"+source, func(t *testing.T) {
				require.Equal(t,
					relationParityItem(t, entry.legacy, "relation-parity-no-route", attribute),
					relationParityItem(t, entry.v3, "relation-parity-no-route", attribute),
				)
			})
		}
	}
}

func TestRelationCompatReversedRangeWindow(t *testing.T) {
	useRelationParityEnvironment(t)
	query := `{"start_time":1700000060,"end_time":1700000000,"step":"60s","source_type":"pod","target_type":"node","source_info":{"bcs_cluster_id":"c","namespace":"ns","pod":"p"}}`
	legacy := relationParityItem(t, HandlerAPIRelationMultiResourceRange, "relation-parity-no-route", query)
	require.Equal(t, float64(http.StatusOK), legacy["code"], legacy["message"])
	got := relationParityItem(t, HandlerAPIRelationV1Beta3MultiResourceRange, "relation-parity-no-route", query)
	require.Equal(t, float64(http.StatusBadRequest), got["code"])
	require.Contains(t, got["message"], "start_time must be less than or equal to end_time")
}
