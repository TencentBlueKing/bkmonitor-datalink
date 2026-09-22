// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
)

func TestTopologyHandlerBudgets(t *testing.T) {
	log.InitTestLogger()
	for _, test := range []struct{ name, body, reason string }{
		{name: "请求体过大", body: `{"padding":"` + strings.Repeat("x", 1024*1024) + `"}`, reason: "topology request exceeds"},
		{name: "批次数量过大", body: `{"query_list":[` + strings.Repeat(`{},`, 16) + `{}]}`, reason: "max_topology_queries"},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(test.body))
			HandlerAPIRelationV1Beta3Topology(c)
			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Contains(t, w.Body.String(), test.reason)
		})
	}
	old := v1beta3.MaxSharedTopologyOutputBytes
	v1beta3.MaxSharedTopologyOutputBytes = 1024
	t.Cleanup(func() { v1beta3.MaxSharedTopologyOutputBytes = old })
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"query_list":[{}]}`))
	HandlerAPIRelationV1Beta3Topology(c)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "max_topology_output_bytes")
}

type topologyAdmissionWriter struct {
	*httptest.ResponseRecorder
	t *testing.T
}

func (w *topologyAdmissionWriter) Write(body []byte) (int, error) {
	_, release, err := v1beta3.AcquireSharedTopology(context.Background())
	if err == nil {
		release()
	}
	require.ErrorContains(w.t, err, "max_topology_concurrency", "响应写出前必须仍占有准入名额")
	return w.ResponseRecorder.Write(body)
}

func TestTopologyAdmissionHeldThroughProxyResponse(t *testing.T) {
	log.InitTestLogger()
	old := v1beta3.MaxSharedTopologyConcurrency
	v1beta3.MaxSharedTopologyConcurrency = 1
	t.Cleanup(func() { v1beta3.MaxSharedTopologyConcurrency = old })
	const route = "/test-topology-proxy-admission"
	metadata.AddHandler(route, HandlerAPIRelationV1Beta3Topology)
	w := &topologyAdmissionWriter{ResponseRecorder: httptest.NewRecorder(), t: t}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/proxy", strings.NewReader(`{"path":"`+route+`","data":{"query_list":[]}}`))
	proxy.HandleProxy(c)
	require.Equal(t, http.StatusOK, w.Code)
	_, release, err := v1beta3.AcquireSharedTopology(context.Background())
	require.NoError(t, err)
	release()
}
