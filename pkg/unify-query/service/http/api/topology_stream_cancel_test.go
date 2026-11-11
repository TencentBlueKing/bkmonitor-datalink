// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
)

var errTopologyTestWrite = errors.New("topology test write failure")

// topologyHookWriter 写出 trigger 命中的片段后取消请求，或从命中的片段起返回传输错误。
type topologyHookWriter struct {
	*httptest.ResponseRecorder
	trigger func([]byte) bool
	cancel  context.CancelFunc
	fail    bool
	failing bool
}

func (w *topologyHookWriter) Write(p []byte) (int, error) {
	if w.failing || (w.fail && w.trigger(p)) {
		w.failing = true
		return 0, errTopologyTestWrite
	}
	n, err := w.ResponseRecorder.Write(p)
	if w.cancel != nil && w.trigger(p) {
		w.cancel()
		w.cancel = nil
	}
	return n, err
}

// itemLead 命中第二项之前的分隔符：它与该项的编码片段在同一次 item 写出中依次写出。
func itemLead(p []byte) bool { return string(p) == "," }

func topologyStreamTestBody(items int) string {
	list := make([]string, items)
	for i := range list {
		list[i] = `{"timestamp":1700000000,"source_type":"unknown-resource"}`
	}
	return `{"query_list":[` + strings.Join(list, ",") + `]}`
}

func useTopologyStreamTestEnvironment(t *testing.T) {
	useRelationTestConfig(t)
	log.InitTestLogger()
	metadata.InitMetadata()
	previous := v1beta3.GetUnderlyingProvider(v1beta3.GetSchemaProvider())
	v1beta3.InitSchemaProvider(nil)
	t.Cleanup(func() { v1beta3.InitSchemaProvider(previous) })
}

// 取消落在一项的两个片段之间（分隔符已写出、项的编码片段尚未写出）：这一项照常写完，
// 取消由下一项之前的检查处理，后续各项按单项 400 写出，响应是完整 JSON。
func TestTopologyCancellationInsideAnItemFinishesThatItem(t *testing.T) {
	useTopologyStreamTestEnvironment(t)
	const items = 3
	body := topologyStreamTestBody(items)
	for _, proxied := range []bool{false, true} {
		t.Run(fmt.Sprintf("proxy=%t", proxied), func(t *testing.T) {
			ctx, cancel := context.WithCancel(metadata.InitHashID(context.Background()))
			defer cancel()
			metadata.SetUser(ctx, &metadata.User{SpaceUID: "space"})
			writer := &topologyHookWriter{ResponseRecorder: httptest.NewRecorder(), trigger: itemLead, cancel: cancel}
			c, _ := gin.CreateTestContext(writer)
			if proxied {
				const route = "/test-topology-cancel-inside-item"
				if _, registered := metadata.GetHandler(route); !registered {
					metadata.AddHandler(route, HandlerAPIRelationV1Beta3Topology)
				}
				c.Request = httptest.NewRequest(http.MethodPost, "/proxy", strings.NewReader(`{"path":"`+route+`","data":`+body+`}`)).WithContext(ctx)
				proxy.HandleProxy(c)
			} else {
				c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body)).WithContext(ctx)
				HandlerAPIRelationV1Beta3Topology(c)
			}
			require.Error(t, ctx.Err(), "the request is canceled while the second item is being written")
			require.Equal(t, http.StatusOK, writer.Code)
			payload := writer.Body.Bytes()
			require.True(t, stdjson.Valid(payload), writer.Body.String())
			if proxied {
				var envelope struct {
					Result bool               `json:"result"`
					Data   stdjson.RawMessage `json:"data"`
				}
				require.NoError(t, stdjson.Unmarshal(payload, &envelope))
				require.True(t, envelope.Result)
				payload = envelope.Data
			}
			var response struct {
				Data []cmdb.SharedTopologyResponseData `json:"data"`
			}
			require.NoError(t, stdjson.Unmarshal(payload, &response))
			require.Len(t, response.Data, items)
			first := response.Data[0]
			require.NotContains(t, first.Message, "context canceled")
			require.Equal(t, first, response.Data[1], "the item being written when the request is canceled is written completely")
			require.Equal(t, http.StatusBadRequest, response.Data[2].Code)
			require.Contains(t, response.Data[2].Message, "context canceled")
			require.NoError(t, proxy.ResponseWriteError(c))
		})
	}
}

// 传输错误使批次停止；API 请求计数与 ResponseWriteError 一样记为失败。
func TestTopologyWriteFailureCountsTheRequestAsFailed(t *testing.T) {
	useTopologyStreamTestEnvironment(t)
	const api = "/test-topology-write-failure"
	count := func(status string) float64 {
		families, err := prometheus.DefaultGatherer.Gather()
		require.NoError(t, err)
		total := 0.0
		for _, family := range families {
			if family.GetName() != "unify_query_api_request_total" {
				continue
			}
			for _, item := range family.GetMetric() {
				labels := map[string]string{}
				for _, label := range item.GetLabel() {
					labels[label.GetName()] = label.GetValue()
				}
				if labels["api"] == api && labels["status"] == status {
					total += item.GetCounter().GetValue()
				}
			}
		}
		return total
	}
	failedBefore, successBefore := count("failed"), count("success")
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "space"})
	writer := &topologyHookWriter{ResponseRecorder: httptest.NewRecorder(), trigger: itemLead, fail: true}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, api, strings.NewReader(topologyStreamTestBody(3))).WithContext(ctx)
	HandlerAPIRelationV1Beta3Topology(c)
	require.ErrorIs(t, proxy.ResponseWriteError(c), errTopologyTestWrite)
	require.Equal(t, failedBefore+1, count("failed"))
	require.Equal(t, successBefore, count("success"))
}
