// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
)

func TestElasticSearchMetaClusterInfoIsLogsDB(t *testing.T) {
	var shipper config.MetaClusterInfo
	err := json.Unmarshal([]byte(`{
		"cluster_type": "elasticsearch",
		"storage_config": {
			"base_index": "2_bklog_test",
			"index_settings": {
				"index.mode": "logsdb",
				"index.mapping.ignore_malformed": true
			}
		}
	}`), &shipper)
	require.NoError(t, err)
	require.NoError(t, shipper.Clean())
	require.True(t, shipper.AsElasticSearchCluster().IsLogsDB())

	shipper.StorageConfig["index_settings"] = map[string]interface{}{"index.mode": "standard"}
	require.False(t, shipper.AsElasticSearchCluster().IsLogsDB())
	shipper.StorageConfig["index_settings"] = "invalid"
	require.False(t, shipper.AsElasticSearchCluster().IsLogsDB())
}
