// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type topologyHTTPQueueResult struct {
	code int
	body string
	err  error
}

func TestTopologyHTTPQueueLifecycle(t *testing.T) {
	log.InitTestLogger()
	metadata.InitMetadata()
	gin.SetMode(gin.ReleaseMode)
	oldLimit := v1beta3.MaxSharedTopologyConcurrent
	v1beta3.MaxSharedTopologyConcurrent = 1
	t.Cleanup(func() { v1beta3.MaxSharedTopologyConcurrent = oldLimit })
	for _, proxied := range []bool{false, true} {
		for _, outcome := range []string{"promote", "disconnect", "deadline", "queue_full"} {
			t.Run(fmt.Sprintf("proxy=%t/%s", proxied, outcome), func(t *testing.T) {
				_, holderRelease, err := v1beta3.AcquireSharedTopology(context.Background())
				require.NoError(t, err)
				t.Cleanup(holderRelease)
				done := make(chan bool, 2)
				path := "/test-topology-queue-" + outcome
				if proxied {
					metadata.AddHandler(path, HandlerAPIRelationV1Beta3TopologyRange)
				}
				route := gin.New()
				route.POST("/query", func(c *gin.Context) {
					if outcome == "deadline" {
						ctx, cancel := context.WithTimeout(c.Request.Context(), 100*time.Millisecond)
						defer cancel()
						c.Request = c.Request.WithContext(ctx)
					}
					if proxied {
						proxy.HandleProxy(c)
					} else {
						HandlerAPIRelationV1Beta3TopologyRange(c)
					}
					data, _ := c.Get(proxy.ContextKeyResponseData)
					done <- data == nil
				})
				server := httptest.NewServer(route)
				defer server.Close()
				client := server.Client()
				client.Timeout = 3 * time.Second
				send := func(ctx context.Context) topologyHTTPQueueResult {
					body := `{"query_list":[]}`
					if proxied {
						body = `{"path":"` + path + `","data":` + body + `}`
					}
					req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/query", strings.NewReader(body))
					if err != nil {
						return topologyHTTPQueueResult{err: err}
					}
					req.Header.Set("Content-Type", "application/json")
					resp, err := client.Do(req)
					if err != nil {
						return topologyHTTPQueueResult{err: err}
					}
					defer resp.Body.Close()
					raw, err := io.ReadAll(resp.Body)
					return topologyHTTPQueueResult{resp.StatusCode, string(raw), err}
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				reply := make(chan topologyHTTPQueueResult, 1)
				go func() { reply <- send(ctx) }()
				require.Eventually(t, func() bool {
					s := topologyTestAdmissionState(t)
					return s["active"] == 1 && s["waiters"] == 1 && s["reserved"] > 0
				}, time.Second, time.Millisecond)
				require.Positive(t, topologyTestAdmissionState(t)["reserved"], "queued request body is counted without executing graph work")
				if outcome == "queue_full" {
					full := send(context.Background())
					require.NoError(t, full.err)
					require.Equal(t, http.StatusBadRequest, full.code)
					require.Contains(t, full.body, "max_topology_admission_waiters")
					require.True(t, <-done, "proxy failure must not retain its payload")
				}
				switch outcome {
				case "promote", "queue_full":
					holderRelease()
				case "disconnect":
					cancel()
				}
				var result topologyHTTPQueueResult
				select {
				case result = <-reply:
				case <-time.After(4 * time.Second):
					t.Fatal("queued HTTP did not finish")
				}
				if outcome == "disconnect" {
					require.ErrorIs(t, result.err, context.Canceled)
				} else {
					require.NoError(t, result.err)
					if outcome == "deadline" {
						require.Equal(t, 400, result.code)
						require.Contains(t, result.body, "deadline exceeded")
					} else {
						require.Equal(t, 200, result.code)
						require.Contains(t, result.body, `"data":[]`)
					}
				}
				select {
				case cleared := <-done:
					require.True(t, cleared, "proxy payload must be cleared before returning its context to the pool")
				case <-time.After(time.Second):
					t.Fatal("handler retained after client exit")
				}
				holderRelease()
				require.Equal(t, map[string]int64{"active": 0, "waiters": 0, "reserved": 0, "materialized": 0}, topologyTestAdmissionState(t))
				// A subsequent request on the same transport remains usable.
				healthy := send(context.Background())
				require.NoError(t, healthy.err)
				require.Equal(t, 200, healthy.code)
				require.True(t, <-done)
			})
		}
	}
}
