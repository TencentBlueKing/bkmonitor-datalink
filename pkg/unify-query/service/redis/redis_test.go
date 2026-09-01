// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
)

func TestServiceReloadReplacesBindingResolverWatcher(t *testing.T) {
	server := miniredis.RunT(t)
	host, portValue, err := net.SplitHostPort(server.Addr())
	require.NoError(t, err)
	port, err := strconv.Atoi(portValue)
	require.NoError(t, err)

	previousMode := Mode
	previousHost := Host
	previousPort := Port
	previousPassword := Password
	previousMasterName := MasterName
	previousSentinelAddress := SentinelAddress
	previousSentinelPassword := SentinelPassword
	previousDataBase := DataBase
	previousServiceName := ServiceName
	previousDialTimeout := DialTimeout
	previousReadTimeout := ReadTimeout
	previousSchemaProviderType := SchemaProviderType
	t.Cleanup(func() {
		Mode = previousMode
		Host = previousHost
		Port = previousPort
		Password = previousPassword
		MasterName = previousMasterName
		SentinelAddress = previousSentinelAddress
		SentinelPassword = previousSentinelPassword
		DataBase = previousDataBase
		ServiceName = previousServiceName
		DialTimeout = previousDialTimeout
		ReadTimeout = previousReadTimeout
		SchemaProviderType = previousSchemaProviderType
	})

	Mode = "standalone"
	Host = host
	Port = port
	Password = ""
	MasterName = ""
	SentinelAddress = nil
	SentinelPassword = ""
	DataBase = 0
	ServiceName = "binding-watcher-reload-test"
	DialTimeout = time.Second
	ReadTimeout = time.Second
	SchemaProviderType = "static"

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	service := &Service{}
	t.Cleanup(service.Close)

	service.Reload(ctx)
	require.Eventually(t, func() bool {
		return bindingWatcherSubscriptionsEqual(server, 1)
	}, time.Second, 10*time.Millisecond)

	service.Reload(ctx)
	require.Eventually(t, func() bool {
		return bindingWatcherSubscriptionsEqual(server, 1)
	}, time.Second, 10*time.Millisecond)

	service.Close()
	require.Eventually(t, func() bool {
		return bindingWatcherSubscriptionsEqual(server, 0)
	}, time.Second, 10*time.Millisecond)
}

func bindingWatcherSubscriptionsEqual(server *miniredis.Miniredis, expected int) bool {
	counts := server.PubSubNumSub(
		v1beta3.BindingRedisChannel,
		v1beta3.ResultTableDetailChannel,
		v1beta3.BuiltInResultTableDetailChannel,
	)
	for _, count := range counts {
		if count != expected {
			return false
		}
	}
	return true
}
