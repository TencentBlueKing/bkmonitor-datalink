//go:build !race

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package curl

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHttpCurlBufferRetentionBoundaries(t *testing.T) {
	// sync.Pool 在 race 模式会随机丢弃对象；普通构建中固定 P 并关闭 GC，
	// 使这里直接观察 Request 的回收策略，不受调度和垃圾回收影响。
	previousProcs := runtime.GOMAXPROCS(1)
	previousGC := debug.SetGCPercent(-1)
	newBuffer := bufPool.New
	t.Cleanup(func() {
		bufPool = sync.Pool{New: newBuffer}
		debug.SetGCPercent(previousGC)
		runtime.GOMAXPROCS(previousProcs)
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	for _, capacity := range []int{1024, 1024 * 1024, 1024*1024 + 1} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			bufPool = sync.Pool{New: newBuffer}
			buffer := bytes.NewBuffer(make([]byte, 0, capacity))
			bufPool.Put(buffer)
			var result map[string]bool
			_, err := (&HttpCurl{}).Request(context.Background(), Get, Options{UrlPath: server.URL}, &result)
			require.NoError(t, err)
			require.True(t, result["ok"])
			next := bufPool.Get().(*bytes.Buffer)
			if capacity <= 1024*1024 {
				require.Same(t, buffer, next)
			} else {
				require.NotSame(t, buffer, next)
				require.LessOrEqual(t, next.Cap(), 1024*1024)
			}
		})
	}
}
