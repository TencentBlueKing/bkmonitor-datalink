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

	"github.com/stretchr/testify/assert"

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
	db.Delete(&f1, "table_id = ?", tableID)
	err := f1.Create(db)
	assert.NoError(t, err)
	err = f2.Create(db)
	assert.NoError(t, err)
	fields, err := NewResultTableFieldSvc(nil).BatchGetFields([]string{tableID}, false)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(fields[tableID]))
}
