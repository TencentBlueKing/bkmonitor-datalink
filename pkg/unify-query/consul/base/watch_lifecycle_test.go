// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package base

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCloseStopsAllWatchesForTheSamePath(t *testing.T) {
	var pending atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("index") != "" {
			pending.Add(1)
			defer pending.Add(-1)
			<-r.Context().Done()
			return
		}
		w.Header().Set("X-Consul-Index", "1")
		_, _ = fmt.Fprint(w, `[{"Key":"test/config","Value":"e30=","ModifyIndex":1}]`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "", "", "")
	require.NoError(t, err)
	defer client.Close()
	first, err := client.Watch("test/config", "")
	require.NoError(t, err)
	second, err := client.Watch("test/config", "")
	require.NoError(t, err)
	require.Eventually(t, func() bool { return pending.Load() == 2 }, 3*time.Second, time.Millisecond)
	require.NoError(t, client.Close())
	require.Eventually(t, func() bool { return pending.Load() == 0 }, 3*time.Second, time.Millisecond)
	for _, channel := range []<-chan any{first, second} {
		done := make(chan struct{})
		go func(ch <-chan any) {
			for range ch {
			}
			close(done)
		}(channel)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("watch did not exit after its client closed")
		}
	}
	_, err = client.Watch("test/config", "")
	require.ErrorContains(t, err, "closed")
}
