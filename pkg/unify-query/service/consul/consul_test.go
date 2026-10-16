// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package consul

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	consulClient "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
)

func TestConsulCanBeDisabledAndReenabled(t *testing.T) {
	viper.Reset()
	service := &Service{}
	service.Close()
	t.Cleanup(func() { service.Close(); service.Wait(); viper.Reset() })
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	viper.Set(config.ConsulEnabledConfigPath, false)
	viper.Set(AddressConfigPath, ":invalid-address")
	LoadConfig()
	service.Reload(context.Background())
	_, err := consulClient.GetFeatureFlagsContext(context.Background())
	require.ErrorContains(t, err, "disabled or not initialized")
	require.Zero(t, requests.Load())
	service.Close()
	service.Wait()

	for _, enabled := range []bool{true, false, true} {
		viper.Set(config.ConsulEnabledConfigPath, enabled)
		viper.Set(AddressConfigPath, server.URL)
		LoadConfig()
		service.Reload(context.Background())
		before := requests.Load()
		_, err := consulClient.GetFeatureFlagsContext(context.Background())
		if enabled {
			require.NoError(t, err)
			require.Equal(t, before+1, requests.Load())
		} else {
			require.ErrorContains(t, err, "disabled or not initialized")
			require.Equal(t, before, requests.Load())
		}
	}
}
