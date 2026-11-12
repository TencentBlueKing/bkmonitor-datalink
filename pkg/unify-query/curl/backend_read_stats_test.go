// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package curl

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// 收到响应的读取（含非 200 与超过边界的响应）计入请求级汇总：次数、响应字节与首字节前等待；
// 没有汇总的 context 不记录。
func TestBackendReadStatsSummarizeResponses(t *testing.T) {
	body := `{"payload":"` + strings.Repeat("x", 100) + `"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	ctx, stats := metadata.WithBackendReadStats(context.Background())
	var decoded map[string]any
	_, err := (&HttpCurl{}).Request(ctx, Get, Options{UrlPath: server.URL}, &decoded)
	require.NoError(t, err)
	_, err = (&HttpCurl{}).Request(metadata.WithBackendResponseLimit(ctx, 16), Get, Options{UrlPath: server.URL}, &decoded)
	require.Error(t, err)
	_, err = (&HttpCurl{}).Request(ctx, Get, Options{UrlPath: server.URL + "/fail"}, &decoded)
	require.Error(t, err)
	count, bytes, firstByte := stats.Snapshot()
	require.Equal(t, int64(3), count)
	require.Equal(t, int64(len(body)+17), bytes, "the rejected read counts the bytes it read")
	require.Positive(t, firstByte)

	_, err = (&HttpCurl{}).Request(context.Background(), Get, Options{UrlPath: server.URL}, &decoded)
	require.NoError(t, err)
	again, _, _ := stats.Snapshot()
	require.Equal(t, count, again)
}
