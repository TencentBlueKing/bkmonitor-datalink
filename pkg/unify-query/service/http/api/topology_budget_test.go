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
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
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
			reason := "max_topology_queries"
			if test.name == "请求体过大" {
				reason = "max_topology_request_bytes"
			}
			before := topologyObservation(t, "cmdb_topology_rejections_total", map[string]string{"query_mode": "instant", "reason": reason}).GetCounter().GetValue()
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(test.body))
			HandlerAPIRelationV1Beta3Topology(c)
			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Contains(t, w.Body.String(), test.reason)
			require.Equal(t, before+1, topologyObservation(t, "cmdb_topology_rejections_total", map[string]string{"query_mode": "instant", "reason": reason}).GetCounter().GetValue())
			require.Zero(t, topologyObservation(t, "cmdb_topology_admission_active", nil).GetGauge().GetValue())
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
	require.Equal(w.t, 1.0, topologyObservation(w.t, "cmdb_topology_admission_active", nil).GetGauge().GetValue())
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

func topologyObservation(t *testing.T, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() != "unify_query_"+name {
			continue
		}
		for _, m := range f.Metric {
			matched := len(m.Label) == len(labels)
			for _, l := range m.Label {
				matched = matched && labels[l.GetName()] == l.GetValue()
			}
			if matched {
				return m
			}
		}
	}
	return &dto.Metric{}
}

func TestTopologyEncodedItemObservations(t *testing.T) {
	log.InitTestLogger()
	labels := map[string]string{"stage": "response-item", "result": "success"}
	before := topologyObservation(t, "cmdb_topology_payload_bytes", labels).GetHistogram()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"query_list":[{},{}]}`))
	HandlerAPIRelationV1Beta3Topology(c)
	require.Equal(t, http.StatusOK, w.Code)
	after := topologyObservation(t, "cmdb_topology_payload_bytes", labels).GetHistogram()
	require.Equal(t, before.GetSampleCount()+2, after.GetSampleCount())
	require.Greater(t, after.GetSampleSum(), before.GetSampleSum())
}
