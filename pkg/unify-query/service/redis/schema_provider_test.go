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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

// Redis 连不上时服务照常启动：关联关系定义以内置定义启动，覆盖层标为不可用，并带上失败原因。
func TestReloadWithUnreachableRedisServesBuiltinRelationSchema(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())

	previousMode, previousHost, previousPort := Mode, Host, Port
	previousDial, previousRead := DialTimeout, ReadTimeout
	previousProvider := v1beta3.GetUnderlyingProvider(v1beta3.GetSchemaProvider())
	t.Cleanup(func() {
		Mode, Host, Port = previousMode, previousHost, previousPort
		DialTimeout, ReadTimeout = previousDial, previousRead
		v1beta3.InitSchemaProvider(previousProvider)
	})
	Mode, Host, Port = "standalone", "127.0.0.1", port
	DialTimeout, ReadTimeout = 50*time.Millisecond, 50*time.Millisecond

	service := &Service{}
	service.Reload(context.Background())
	t.Cleanup(service.Close)

	schema, ok := v1beta3.EffectiveSchema("bkcc__2")
	require.True(t, ok)
	require.Equal(t, "redis", schema.Provider)
	require.Equal(t, relation.OverlayStateUnavailable, schema.Overlay.State)
	require.Equal(t, relation.OverlayReasonReadFailed, schema.Overlay.LastReason)
	require.Positive(t, schema.Overlay.Failures[relation.OverlayReasonReadFailed])
	require.Equal(t, []string{"bcs_cluster_id", "namespace", "pod"},
		v1beta3.GetSchemaProvider().GetResourcePrimaryKeys("bkcc__2", "pod"))
}
