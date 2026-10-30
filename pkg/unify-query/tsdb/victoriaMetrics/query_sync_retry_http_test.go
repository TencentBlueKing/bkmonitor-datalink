// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package victoriaMetrics

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

func TestVMQueryDoesNotRetryHTTPStatusContainingConnectionError(t *testing.T) {
	previousTransport := http.DefaultTransport
	transport := &http.Transport{}
	http.DefaultTransport = transport
	t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = previousTransport })
	for _, message := range []string{
		"http2: client connection force closed via ClientConn.Close",
		`http2: server sent GOAWAY and closed the connection; LastStreamID=1, ErrCode=NO_ERROR, debug=""`,
	} {
		t.Run(message, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				conn, writer, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				// 发送真实 HTTP 状态行；reason phrase 可能来自代理的错误描述。
				_, err = fmt.Fprintf(writer, "HTTP/1.1 503 %s\r\nConnection: close\r\nContent-Length: 0\r\n\r\n", message)
				if err == nil {
					err = writer.Flush()
				}
				if err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			instance := &Instance{url: server.URL, timeout: time.Second, curl: &curl.HttpCurl{}}
			ctx, span := trace.NewSpan(metadata.InitHashID(context.Background()), "http-status-retry")
			err := instance.vmQuery(ctx, "{}", &VmResponse{}, span)
			span.End(&err)
			var status *metadata.BackendStatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, http.StatusServiceUnavailable, status.Status)
			require.Equal(t, int32(1), calls.Load(), "HTTP 状态错误不应触发连接故障重试")
		})
	}
}
