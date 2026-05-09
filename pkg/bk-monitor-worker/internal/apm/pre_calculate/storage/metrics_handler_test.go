// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestBkBizIdToSpaceUID(t *testing.T) {
	mocker.InitTestDBConfig("../../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	// 使用原生SQL插入测试数据，补全数据库表中所有NOT NULL字段
	testIds := []int{90001, 90002, 90003}
	insertSQL := "INSERT INTO metadata_space (id, space_type_id, space_id, space_name, status, time_zone, language, is_bcs_valid, is_global, creator, create_time, updater, update_time) VALUES (?, ?, ?, ?, 'normal', 'Asia/Shanghai', 'zh-hans', 0, 0, 'test', NOW(), 'test', NOW())"
	require.NoError(t, db.Exec(insertSQL, testIds[0], "bkcc", "100", "test_bkcc_space").Error)
	require.NoError(t, db.Exec(insertSQL, testIds[1], "bkci", "test_project", "test_bkci_space").Error)
	require.NoError(t, db.Exec(insertSQL, testIds[2], "bksaas", "test_app", "test_bksaas_space").Error)
	defer func() {
		for _, id := range testIds {
			db.Exec("DELETE FROM metadata_space WHERE id = ?", id)
		}
	}()

	t.Run("positive_bizId_returns_bkcc_spaceUID", func(t *testing.T) {
		assert.Equal(t, "bkcc__100", bkBizIdToSpaceUID("100"))
		assert.Equal(t, "bkcc__2", bkBizIdToSpaceUID("2"))
		assert.Equal(t, "bkcc__999", bkBizIdToSpaceUID("999"))
	})

	t.Run("negative_bizId_returns_correct_spaceUID", func(t *testing.T) {
		// -90002 -> Space.Id=90002 -> bkci__test_project
		assert.Equal(t, "bkci__test_project", bkBizIdToSpaceUID("-90002"))
		// -90003 -> Space.Id=90003 -> bksaas__test_app
		assert.Equal(t, "bksaas__test_app", bkBizIdToSpaceUID("-90003"))
	})

	t.Run("negative_bizId_not_found_returns_empty", func(t *testing.T) {
		assert.Equal(t, "", bkBizIdToSpaceUID("-99999"))
	})

	t.Run("zero_bizId_returns_empty", func(t *testing.T) {
		assert.Equal(t, "", bkBizIdToSpaceUID("0"))
	})

	t.Run("empty_string_returns_empty", func(t *testing.T) {
		assert.Equal(t, "", bkBizIdToSpaceUID(""))
	})

	t.Run("invalid_string_returns_empty", func(t *testing.T) {
		assert.Equal(t, "", bkBizIdToSpaceUID("abc"))
	})
}
