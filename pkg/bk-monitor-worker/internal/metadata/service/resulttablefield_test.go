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
	"time"

	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestResultTableFieldSvc_BatchGetFields(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	tableID := "test_table_001.base"
	f1 := resulttable.ResultTableField{
		TableID:        tableID,
		FieldName:      "f1",
		FieldType:      "string",
		IsConfigByUser: true,
		CreateTime:     time.Now(),
		LastModifyTime: time.Now(),
		IsDisabled:     false,
	}
	f2 := resulttable.ResultTableField{
		TableID:        tableID,
		FieldName:      "f2",
		FieldType:      "bool",
		IsConfigByUser: true,
		CreateTime:     time.Now(),
		LastModifyTime: time.Now(),
		IsDisabled:     false,
	}
	db := mysql.GetDBSession().DB
	defer db.Close()
	db.Delete(&f1, "table_id = ?", tableID)
	err := f1.Create(db)
	assert.NoError(t, err)
	err = f2.Create(db)
	assert.NoError(t, err)
	fields, err := NewResultTableFieldSvc(nil).BatchGetFields([]string{tableID}, false)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(fields[tableID]))
}

func TestResultTableFieldSvc_BulkCreateDefaultFields(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	defer db.Close()
	rt := resulttable.ResultTable{
		TableId:        "test_result_table_for_default_fields",
		TableNameZh:    "test_result_table_for_default_fields",
		IsCustomTable:  true,
		SchemaType:     models.ResultTableSchemaTypeFree,
		DefaultStorage: models.StorageTypeInfluxdb,
		BkBizId:        2,
		IsEnable:       true,
	}
	db.Delete(&rt, "table_id = ?", rt.TableId)
	db.Delete(&resulttable.ResultTableField{}, "table_id = ?", rt.TableId)
	db.Delete(&resulttable.ResultTableFieldOption{}, "table_id = ?", rt.TableId)
	err := rt.Create(db)
	assert.NoError(t, err)

	// new create
	err = NewResultTableFieldSvc(nil).BulkCreateDefaultFields(rt.TableId, map[string]interface{}{}, false)
	assert.NoError(t, err)
	var rtfList []resulttable.ResultTableField
	err = resulttable.NewResultTableFieldQuerySet(db).TableIDEq(rt.TableId).All(&rtfList)
	assert.NoError(t, err)
	var rtfNames []string
	for _, field := range rtfList {
		rtfNames = append(rtfNames, field.FieldName)
	}
	assert.Equal(t, 9, len(rtfList))
	assert.ElementsMatch(t, []string{"bk_agent_id", "bk_biz_id", "bk_cloud_id", "bk_cmdb_level", "bk_host_id", "bk_supplier_id", "bk_target_host_id", "ip", "time"}, rtfNames)

	var rtfo resulttable.ResultTableFieldOption
	err = resulttable.NewResultTableFieldOptionQuerySet(db).TableIDEq(rt.TableId).FieldNameEq("bk_cmdb_level").One(&rtfo)
	assert.NoError(t, err)
	assert.Equal(t, "true", rtfo.Value)
	assert.Equal(t, models.RTFOInfluxdbDisabled, rtfo.Name)

	// exist some fields return error
	err = NewResultTableFieldSvc(nil).BulkCreateDefaultFields(rt.TableId, map[string]interface{}{}, false)
	assert.Error(t, err)

	db.Delete(&resulttable.ResultTableField{}, "table_id = ?", rt.TableId)
	db.Delete(&resulttable.ResultTableFieldOption{}, "table_id = ?", rt.TableId)

	// time field only
	err = NewResultTableFieldSvc(nil).BulkCreateDefaultFields(rt.TableId, map[string]interface{}{}, true)
	var rtfList2 []resulttable.ResultTableField
	err = resulttable.NewResultTableFieldQuerySet(db).TableIDEq(rt.TableId).All(&rtfList2)
	assert.NoError(t, err)
	var rtfNames2 []string
	for _, field := range rtfList2 {
		rtfNames2 = append(rtfNames2, field.FieldName)
	}
	assert.NoError(t, err)
	assert.Equal(t, 1, len(rtfList2))
	assert.ElementsMatch(t, []string{"time"}, rtfNames2)

	err = resulttable.NewResultTableFieldOptionQuerySet(db).TableIDEq(rt.TableId).FieldNameEq("bk_cmdb_level").One(&rtfo)
	assert.Error(t, gorm.ErrRecordNotFound, err)
}
