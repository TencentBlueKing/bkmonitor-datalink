// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License. See the License at http://opensource.org/licenses/MIT.

package consul

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	consulUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/consul"
	"github.com/stretchr/testify/require"
)

func TestKMSOptionsReachBothBMWConsulClients(t *testing.T) {
	oldInstance := instance
	t.Cleanup(func() { instance = oldInstance; consulOnce = sync.Once{} })
	instance = nil
	consulOnce = sync.Once{}
	var kvCalls, registrationCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "bmw-test-token", r.Header.Get("X-Consul-Token"))
		user, password, ok := r.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "bmw-test-user", user)
		require.Equal(t, "bmw-test-password", password)
		switch r.URL.Path {
		case "/v1/kv/test":
			atomic.AddInt32(&kvCalls, 1)
			_, _ = w.Write([]byte(`[{"Key":"test","Value":"dmFsdWU=","ModifyIndex":1}]`))
		case "/v1/agent/check/register":
			atomic.AddInt32(&registrationCalls, 1)
			_, _ = w.Write([]byte(`true`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	inst, err := NewInstance(context.Background(), consulUtils.InstanceOptions{Addr: server.URL, ConsulAddr: server.URL, SrvName: "bmw-test", TTL: "10s", ClientOptions: &consulUtils.ClientOptions{Token: "bmw-test-token", Username: "bmw-test-user", Password: "bmw-test-password"}})
	require.NoError(t, err)
	index, value, err := inst.Get("test")
	require.NoError(t, err)
	require.Equal(t, uint64(1), index)
	require.Equal(t, "value", string(value))
	require.NoError(t, inst.Client.CheckRegister())
	require.Equal(t, int32(1), atomic.LoadInt32(&kvCalls))
	require.Equal(t, int32(1), atomic.LoadInt32(&registrationCalls))
}
