// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build elasticsearch_v5 && elasticsearch_v6 && elasticsearch_v7 && elasticsearch_v8 && elasticsearch_v9

package elasticsearch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
)

func TestBulkWriterVersions(t *testing.T) {
	// 直接构造生产处理器，覆盖版本解析、注册和客户端初始化这条调用路径。
	for _, tc := range []struct {
		version string
		writer  BulkWriter
	}{
		{"5.4.0", &ESv5Writer{}},
		{"6.8.23", &ESv6Writer{}},
		{"7.10.2", &ESv7Writer{}},
		{"8.19.6", &ESv8Writer{}},
		{"9.4.3", &ESv9Writer{}},
		{"10.0.0", nil},
		{"invalid", nil},
	} {
		t.Run(tc.version, func(t *testing.T) {
			cluster := config.NewMetaClusterInfo().AsElasticSearchCluster()
			cluster.SetVersion(tc.version)
			cluster.SetSchema("http")
			cluster.SetDomain("127.0.0.1")
			cluster.SetPort(9200)
			handler, err := NewBulkHandler(cluster, &config.MetaResultTableConfig{ResultTable: "doc"}, time.Second, nil, FixedIndexRender("test"))
			if tc.writer == nil {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.IsType(t, tc.writer, handler.writer)
			require.NoError(t, handler.Close())
		})
	}
}
