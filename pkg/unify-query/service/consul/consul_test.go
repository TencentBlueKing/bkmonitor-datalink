// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package consul

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	consulClient "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestConsulKVOnlyLifecycleIgnoresLegacyRegistrationSettings(t *testing.T) {
	viper.Reset()
	service := &Service{}
	service.Close()
	t.Cleanup(func() { service.Close(); service.Wait(); viper.Reset() })
	var requests, agentRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/kv/test/data/feature_flag" {
			agentRequests.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprint(w, `[{"Key":"test/data/feature_flag","Value":"eyJ0ZXN0Ijp0cnVlfQ=="}]`)
	}))
	defer server.Close()
	viper.Set(AddressConfigPath, server.URL)
	viper.Set(KVBasePathConfigPath, "test")
	viper.Set("consul.service_name", "legacy-nonempty-name")
	viper.Set("consul.check_ttl", "30ms")
	for _, enabled := range []bool{true, false, true} {
		// Already published configurations may retain this obsolete setting.
		viper.Set("consul.enabled", enabled)
		LoadConfig()
		before := requests.Load()
		service.Reload(context.Background())
		require.Equal(t, before, requests.Load(), "initialization must not contact Consul")
		data, err := consulClient.GetFeatureFlagsContext(context.Background())
		require.NoError(t, err)
		require.JSONEq(t, `{"test":true}`, string(data))
		require.Equal(t, before+1, requests.Load())
		service.Close()
		service.Wait()
		_, err = consulClient.GetFeatureFlagsContext(context.Background())
		require.ErrorContains(t, err, "not initialized")
	}
	// An old TTL loop would have sent many heartbeats during this interval.
	time.Sleep(100 * time.Millisecond)
	require.EqualValues(t, 3, requests.Load())
	require.Zero(t, agentRequests.Load())
}

func TestConsulKVReloadStopsWatch(t *testing.T) {
	viper.Reset()
	service := &Service{}
	service.Close()
	t.Cleanup(func() { service.Close(); viper.Reset() })
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
	defer service.Close()
	viper.Set(AddressConfigPath, server.URL)
	LoadConfig()
	service.Start(context.Background())
	watch, err := consulClient.WatchChange(context.Background(), "test/config")
	require.NoError(t, err)
	require.Eventually(t, func() bool { return pending.Load() == 1 }, time.Second, time.Millisecond)
	service.Reload(context.Background())
	require.Eventually(t, func() bool { return pending.Load() == 0 }, time.Second, time.Millisecond)
	done := make(chan struct{})
	go func() {
		for range watch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watch remained active after reload")
	}
	data, err := consulClient.GetKVDataContext(context.Background(), "test/config")
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(data))
}
