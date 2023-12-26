// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/influxdata/influxdb1-client/models"
	client "github.com/influxdata/influxdb1-client/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestInfluxdbHostInfoSvc_RefreshDefaultRp(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	hostInfo := storage.InfluxdbHostInfo{
		HostName:   "influxdb_test_host",
		DomainName: "127.0.0.1",
		Port:       8086,
		Status:     true,
		Protocol:   "http",
	}
	db.Delete(&hostInfo, "host_name = ?", hostInfo.HostName)
	err := hostInfo.Create(db)
	assert.NoError(t, err)

	cluster := storage.InfluxdbClusterInfo{
		HostName:     hostInfo.HostName,
		ClusterName:  "influxdb_test_cluster_name",
		HostReadable: true,
	}
	db.Delete(&cluster, "host_name = ?", hostInfo.HostName)
	err = cluster.Create(db)
	assert.NoError(t, err)

	dbStorage := storage.InfluxdbStorage{
		TableID:          "influxdb_test_table",
		RealTableName:    "real_table",
		Database:         "influxdb_test_db_name",
		ProxyClusterName: cluster.ClusterName,
		UseDefaultRp:     true,
		EnableRefreshRp:  true,
	}
	db.Delete(&dbStorage, "proxy_cluster_name = ?", cluster.ClusterName)
	err = dbStorage.Create(db)
	assert.NoError(t, err)
	mockerClient := &mocker.InfluxDBClientMocker{}
	gomonkey.ApplyFunc(InfluxdbHostInfoSvc.GetClient, func(svc InfluxdbHostInfoSvc) (client.Client, error) {
		return mockerClient, nil
	})
	gomonkey.ApplyFunc(influxdb.QueryDB, func(clnt client.Client, cmd string, database string, params map[string]interface{}) ([]client.Result, error) {
		if cmd == "SHOW DATABASES" {
			var results []client.Result
			result := client.Result{
				Series: []models.Row{{
					Name:    "databases",
					Columns: []string{"name"},
					Values:  [][]interface{}{{"db1"}, {"influxdb_test_db_name"}, {"db2"}},
					Partial: false,
				}},
			}
			results = append(results, result)
			return results, nil
		}
		if cmd == "SHOW RETENTION POLICIES" {
			var results []client.Result
			var result client.Result
			if database == "influxdb_test_db_name" {
				result = client.Result{
					Series: []models.Row{{
						Name:    "databases",
						Columns: []string{"name", "duration", "shardGroupDuration", "replicaN", "default"},
						Values:  [][]interface{}{{"newpolicy1", "720h0m0s", "168h0m0s", 1, false}, {"autogen", "722h0m0s", "168h0m0s", 1, true}},
						Partial: false,
					}},
				}
			}
			results = append(results, result)
			return results, nil
		}
		if strings.HasPrefix(cmd, "ALTER RETENTION POLICY") {
			assert.True(t, assert.Equal(t, "ALTER RETENTION POLICY autogen ON influxdb_test_db_name DURATION 720h0m0s SHARD DURATION 7d DEFAULT", cmd))
		}
		return nil, nil
	})
	svc := NewInfluxdbHostInfoSvc(&hostInfo)
	err = svc.RefreshDefaultRp()
	assert.NoError(t, err)
}
