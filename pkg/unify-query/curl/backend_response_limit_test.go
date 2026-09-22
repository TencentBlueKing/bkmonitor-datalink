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
			var decoded map[string]any
			size, err := (&HttpCurl{}).Request(child, Get, Options{UrlPath: server.URL, MaxResponseBytes: 2048}, &decoded)
			var limit *ResponseBodyLimitError
			require.ErrorAs(t, err, &limit)
			require.Equal(t, 1025, size)
			require.Nil(t, decoded)
			require.True(t, metadata.BackendResponseLimitExceeded(ctx))
			require.False(t, metadata.BackendResponseLimitExceeded(context.Background()))
		})
	}
}
