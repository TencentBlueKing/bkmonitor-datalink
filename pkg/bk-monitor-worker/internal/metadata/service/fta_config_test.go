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
	"encoding/json"
	"os"
	"testing"

	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	tc "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
)

// The test uses only connection-local tables in an explicitly selected sandbox database.
func TestFTAMetadata(t *testing.T) {
	dsn := os.Getenv("BMW_FTA_TEST_DSN")
	if dsn == "" {
		t.Skip("BMW_FTA_TEST_DSN is unset")
	}
	db, err := gorm.Open("mysql", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.DB().SetMaxOpenConns(1)
	for _, sql := range []string{
		"CREATE TEMPORARY TABLE metadata_kafkatopicinfo (id INT, bk_data_id INT, topic VARCHAR(32))",
		"CREATE TEMPORARY TABLE metadata_clusterinfo (cluster_id INT,bk_tenant_id VARCHAR(32),domain_name VARCHAR(32),port INT,cluster_type VARCHAR(32))",
		"CREATE TEMPORARY TABLE metadata_datasourceoption (bk_tenant_id VARCHAR(32),bk_data_id INT,name VARCHAR(32),value TEXT,value_type VARCHAR(32))",
		"CREATE TEMPORARY TABLE metadata_datasourceresulttable (bk_tenant_id VARCHAR(32),bk_data_id INT,table_id VARCHAR(64))",
		"CREATE TEMPORARY TABLE metadata_resulttable (bk_tenant_id VARCHAR(32),table_id VARCHAR(64),is_enable BOOL,is_deleted BOOL)",
		"CREATE TEMPORARY TABLE metadata_resulttablefield (bk_tenant_id VARCHAR(32),table_id VARCHAR(64),field_name VARCHAR(32),field_type VARCHAR(32))",
		"CREATE TEMPORARY TABLE metadata_resulttablefieldoption (bk_tenant_id VARCHAR(32),table_id VARCHAR(64),field_name VARCHAR(32))",
		"CREATE TEMPORARY TABLE metadata_kafkastorage (bk_tenant_id VARCHAR(32),table_id VARCHAR(64),topic VARCHAR(64),storage_cluster_id INT)",
		"INSERT INTO metadata_kafkatopicinfo VALUES(1,42,'input')",
		"INSERT INTO metadata_clusterinfo VALUES (1,'other','wrong',9092,'kafka'),(1,'source','broker',9092,'kafka')",
		"INSERT INTO metadata_datasourceresulttable VALUES('source',42,'surprise.base'),('source',42,'disabled.base')",
		"INSERT INTO metadata_resulttable VALUES('source','surprise.base',1,0),('source','disabled.base',0,0),('other','surprise.base',1,0)",
		"INSERT INTO metadata_resulttablefield VALUES('source','surprise.base','alert_name','string'),('other','surprise.base','wrong','string')",
		"INSERT INTO metadata_kafkastorage VALUES ('other','surprise.base','wrong',1),('source','surprise.base','result',1)",
	} {
		require.NoError(t, db.Exec(sql).Error)
	}
	src := resulttable.DataSource{BkDataId: 42, BkTenantId: "source", MqClusterId: 1, DataName: "unrelated"}
	check := func() tc.PipelineConfig {
		raw, err := NewDataSourceSvc(&src).FTAConfig(db)
		require.NoError(t, err)
		b, err := json.Marshal(raw)
		require.NoError(t, err)
		var spec tc.PipelineConfig
		require.NoError(t, json.Unmarshal(b, &spec))
		return spec
	}
	spec := check()
	require.Equal(t, "input", spec.MQConfig.StorageConfig["topic"])
	require.Equal(t, "broker", spec.MQConfig.ClusterConfig["domain_name"])
	require.Len(t, spec.ResultTableList, 1)
	rt := spec.ResultTableList[0]
	require.Equal(t, "surprise.base", rt.ResultTable)
	require.Len(t, rt.FieldList, 1)
	require.Equal(t, "alert_name", rt.FieldList[0].FieldName)
	require.Equal(t, "result", rt.ShipperList[0].StorageConfig["topic"])
	require.NoError(t, db.Exec("UPDATE metadata_resulttable SET is_enable=0").Error)
	require.Empty(t, check().ResultTableList)
}
