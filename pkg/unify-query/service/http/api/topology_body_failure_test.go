// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

type topologyCanceledBody struct{ ctx context.Context }

func (r topologyCanceledBody) Read([]byte) (int, error) {
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}
func (topologyCanceledBody) Close() error { return nil }

func topologyTestMetric(t *testing.T, name string, labels map[string]string) (counter, count, sum float64) {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, item := range family.Metric {
			match := true
			for key, value := range labels {
				found := false
				for _, label := range item.Label {
					if label.GetName() == key && label.GetValue() == value {
						found = true
					}
				}
				match = match && found
			}
			if match {
				return item.GetCounter().GetValue(), float64(item.GetHistogram().GetSampleCount()), item.GetHistogram().GetSampleSum()
			}
		}
	}
	return
}

func TestTopologyBodyFailureHTTP(t *testing.T) {
	log.InitTestLogger()
	metadata.InitMetadata()
	gin.SetMode(gin.ReleaseMode)
	for _, name := range []string{"direct_slow_body", "proxy_inner_cancel_injection", "direct_size_limit", "queued_size_limit"} {
		t.Run(name, func(t *testing.T) {
			oldLimit, oldConcurrent := v1beta3.MaxSharedTopologyRequestBytes, v1beta3.MaxSharedTopologyConcurrent
			v1beta3.MaxSharedTopologyRequestBytes, v1beta3.MaxSharedTopologyConcurrent = 32, 1
			defer func() {
				v1beta3.MaxSharedTopologyRequestBytes, v1beta3.MaxSharedTopologyConcurrent = oldLimit, oldConcurrent
			}()
			var release func()
			if name == "queued_size_limit" {
				_, r, err := v1beta3.AcquireSharedTopology(context.Background())
				require.NoError(t, err)
				release = r
				defer release()
			}
			isLimit := strings.HasSuffix(name, "size_limit")
			reason := "invalid_request"
			if isLimit {
				reason = "max_topology_request_bytes"
			}
			rejections := "unify_query_cmdb_topology_rejections_total"
			payload := "unify_query_cmdb_topology_payload_bytes"
			rejectLabels := map[string]string{"query_mode": "range", "reason": reason}
			payloadLabels := map[string]string{"stage": "request-body", "result": "rejected"}
			beforeReject, _, _ := topologyTestMetric(t, rejections, rejectLabels)
			_, beforeCount, beforeSum := topologyTestMetric(t, payload, payloadLabels)
			done := make(chan struct{})
			route := gin.New()
			if name == "proxy_inner_cancel_injection" {
				// The real proxy fully decodes its socket body before calling topology.
				// This explicit inner-reader injection tests failure wrapping, not a
				// claim that an ordinary proxy body is a slow network reader.
				metadata.AddHandler("/research-body-failure", func(c *gin.Context) {
					c.Request.Body = topologyCanceledBody{ctx: c.Request.Context()}
					HandlerAPIRelationV1Beta3TopologyRange(c)
				})
			}
			route.POST("/query", func(c *gin.Context) {
				defer close(done)
				ctx := metadata.InitHashID(c.Request.Context())
				metadata.SetUser(ctx, &metadata.User{SpaceUID: "space"})
				if !isLimit {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 60*time.Millisecond)
					defer cancel()
				}
				c.Request = c.Request.WithContext(ctx)
				if name == "proxy_inner_cancel_injection" {
					proxy.HandleProxy(c)
				} else {
					HandlerAPIRelationV1Beta3TopologyRange(c)
				}
			})
			server := httptest.NewServer(route)
			defer server.Close()
			var body io.Reader = bytes.NewBufferString(strings.Repeat("a", 64))
			var pipe *io.PipeWriter
			if name == "direct_slow_body" {
				reader, writer := io.Pipe()
				pipe, body = writer, reader
				defer reader.Close()
				defer writer.Close()
				go func() { _, _ = writer.Write([]byte("{")) }()
			} else if name == "proxy_inner_cancel_injection" {
				body = bytes.NewBufferString(`{"path":"/research-body-failure","data":{}}`)
			}
			client := server.Client()
			client.Timeout = 2 * time.Second
			resp, err := client.Post(server.URL+"/query", "application/json", body)
			if pipe != nil {
				_ = pipe.Close()
			}
			require.NoError(t, err)
			raw, err := io.ReadAll(resp.Body)
			require.NoError(t, resp.Body.Close())
			require.NoError(t, err)
			require.Equal(t, 400, resp.StatusCode)
			require.True(t, json.Valid(raw), string(raw))
			if isLimit {
				require.Contains(t, string(raw), "maximum size of 32 bytes")
			} else {
				require.Contains(t, string(raw), "deadline exceeded")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("handler did not exit")
			}
			if release != nil {
				release()
			}
			require.Equal(t, map[string]int64{"active": 0, "waiters": 0, "reserved": 0, "materialized": 0}, topologyTestAdmissionState(t))
			afterReject, _, _ := topologyTestMetric(t, rejections, rejectLabels)
			_, afterCount, afterSum := topologyTestMetric(t, payload, payloadLabels)
			require.Equal(t, float64(1), afterReject-beforeReject)
			require.Equal(t, float64(1), afterCount-beforeCount)
			if isLimit {
				require.Equal(t, float64(33), afterSum-beforeSum)
			}
			fmt.Printf("BODY_FAILURE_RESULT name=%s status=%d bytes=%d rejections_delta=%.0f payload_count_delta=%.0f payload_bytes=%.0f final=%v\n", name, resp.StatusCode, len(raw), afterReject-beforeReject, afterCount-beforeCount, afterSum-beforeSum, topologyTestAdmissionState(t))
		})
	}
}

func topologyTestAdmissionState(t *testing.T) map[string]int64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	names := map[string]string{
		"unify_query_cmdb_topology_admission_active":   "active",
		"unify_query_cmdb_topology_admission_waiters":  "waiters",
		"unify_query_cmdb_topology_reserved_bytes":     "reserved",
		"unify_query_cmdb_topology_materialized_bytes": "materialized",
	}
	state := make(map[string]int64, len(names))
	for _, family := range families {
		if key, ok := names[family.GetName()]; ok {
			require.Len(t, family.Metric, 1)
			state[key] = int64(family.Metric[0].GetGauge().GetValue())
		}
	}
	require.Len(t, state, len(names))
	return state
}
