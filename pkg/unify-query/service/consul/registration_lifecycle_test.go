// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package consul

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	consulClient "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
)

func TestRegistrationStopsOnDisableAndRestartsOnEnable(t *testing.T) {
	viper.Reset()
	service := &Service{}
	service.Close()
	t.Cleanup(func() { service.Close(); service.Wait(); viper.Reset() })

	var mu sync.Mutex
	requests := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind := "unexpected"
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/agent/health/service/id/"):
			kind = "health-read"
		case r.URL.Path == "/v1/agent/service/register":
			kind = "service-register"
		case r.URL.Path == "/v1/agent/check/register":
			kind = "check-register"
		case strings.HasPrefix(r.URL.Path, "/v1/agent/check/pass/"):
			kind = "heartbeat"
		case strings.HasPrefix(r.URL.Path, "/v1/agent/check/deregister/"):
			kind = "check-deregister"
		case strings.HasPrefix(r.URL.Path, "/v1/agent/service/deregister/"):
			kind = "service-deregister"
		}
		mu.Lock()
		requests[kind]++
		mu.Unlock()
		if kind == "health-read" {
			// The SDK translates a missing service into (critical, nil, nil),
			// which makes ServiceAwake register the service before its TTL check.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	snapshot := func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		result := make(map[string]int, len(requests))
		for kind, count := range requests {
			result[kind] = count
		}
		return result
	}
	reload := func() {
		LoadConfig()
		done := make(chan struct{})
		go func() {
			service.Reload(context.Background())
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Consul reload did not finish")
		}
	}

	viper.Set(AddressConfigPath, server.URL)
	viper.Set(ServiceNameConfigPath, "registration-lifecycle-test")
	viper.Set(HTTPAddressConfigPath, "127.0.0.1")
	viper.Set(PortConfigPath, 19099)
	viper.Set(TTLConfigPath, "300ms")
	viper.Set(KVBasePathConfigPath, "registration-lifecycle-test")

	previousHeartbeats := 0
	for cycle := 1; cycle <= 2; cycle++ {
		viper.Set(config.ConsulEnabledConfigPath, true)
		reload()
		require.Eventually(t, func() bool {
			return snapshot()["heartbeat"] > previousHeartbeats
		}, 3*time.Second, time.Millisecond)
		running := snapshot()
		require.Equal(t, cycle, running["health-read"])
		require.Equal(t, cycle, running["service-register"])
		require.Equal(t, cycle, running["check-register"])

		viper.Set(config.ConsulEnabledConfigPath, false)
		reload()
		stopped := snapshot()
		require.Equal(t, cycle, stopped["check-deregister"])
		require.Equal(t, cycle, stopped["service-deregister"])
		require.Zero(t, stopped["unexpected"])
		_, err := consulClient.GetFeatureFlagsContext(context.Background())
		require.ErrorContains(t, err, "disabled or not initialized")
		// A tick occurs every TTL/3. Check more than three ticks to detect
		// lingering heartbeats as well as accesses through the disabled client.
		require.Never(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			for kind, count := range requests {
				if count != stopped[kind] {
					return true
				}
			}
			return false
		}, 350*time.Millisecond, 10*time.Millisecond)
		previousHeartbeats = stopped["heartbeat"]
	}
}
