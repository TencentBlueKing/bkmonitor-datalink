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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestResultTableOptionSvc_BulkCreateOptions(t *testing.T) {
	config.FilePath = "../../../bmw.yaml"
	mocker.PatchDBSession()
	db := mysql.GetDBSession().DB
	rt := resulttable.ResultTable{
		TableId:        "test_table_for_rto",
		TableNameZh:    "test_table_for_rto",
		IsCustomTable:  true,
		SchemaType:     models.ResultTableSchemaTypeFree,
		DefaultStorage: models.StorageTypeInfluxdb,
		BkBizId:        2,
		IsEnable:       true,
	}
	db.Delete(&rt, "table_id = ?", rt.TableId)
	db.Delete(&resulttable.ResultTableOption{}, "table_id = ?", rt.TableId)

	err := rt.Create(db)
	assert.NoError(t, err)

	// create
	err = NewResultTableOptionSvc(nil).BulkCreateOptions(rt.TableId, map[string]interface{}{"is_split_measurement": true}, "test")
	assert.NoError(t, err)

	// exist
	err = NewResultTableOptionSvc(nil).BulkCreateOptions(rt.TableId, map[string]interface{}{"is_split_measurement": true}, "test")
	assert.Error(t, err)
}
