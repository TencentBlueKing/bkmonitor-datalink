// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package curl

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestBackendResponseContextLimit(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(fmt.Sprint(compressed), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var out io.Writer = w
				if compressed {
					w.Header().Set("Content-Encoding", "gzip")
					gz := gzip.NewWriter(w)
					defer gz.Close()
					out = gz
				}
				_, _ = io.WriteString(out, `{"payload":"`+strings.Repeat("x", 8192)+`"}`)
			}))
			defer server.Close()
			ctx := metadata.WithBackendResponseLimit(context.Background(), 1024)
			child, cancel := context.WithCancel(ctx)
			defer cancel()
			beforeCount, beforeBytes := backendPayloadObservation(t)
			var decoded map[string]any
			size, err := (&HttpCurl{}).Request(child, Get, Options{UrlPath: server.URL, MaxResponseBytes: 2048}, &decoded)
			var limit *ResponseBodyLimitError
			require.ErrorAs(t, err, &limit)
			require.Equal(t, 1025, size)
			count, bytes := backendPayloadObservation(t)
			require.Equal(t, beforeCount+1, count)
			require.Equal(t, beforeBytes+1025, bytes)
			require.Nil(t, decoded)
			require.True(t, metadata.BackendResponseLimitExceeded(ctx))
			require.False(t, metadata.BackendResponseLimitExceeded(context.Background()))
			// 普通调用即使有 Options 限额，也不进入拓扑专用字节指标。
			_, ordinaryErr := (&HttpCurl{}).Request(context.Background(), Get, Options{UrlPath: server.URL, MaxResponseBytes: 1024}, &decoded)
			require.ErrorAs(t, ordinaryErr, &limit)
			ordinaryCount, ordinaryBytes := backendPayloadObservation(t)
			require.Equal(t, count, ordinaryCount)
			require.Equal(t, bytes, ordinaryBytes)
		})
	}
}

func backendPayloadObservation(t *testing.T) (uint64, float64) {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() != "unify_query_cmdb_topology_payload_bytes" {
			continue
		}
		for _, m := range f.Metric {
			labels := map[string]string{}
			for _, l := range m.Label {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["stage"] == "backend-response" && labels["result"] == "rejected" {
				return m.GetHistogram().GetSampleCount(), m.GetHistogram().GetSampleSum()
			}
		}
	}
	return 0, 0
}
