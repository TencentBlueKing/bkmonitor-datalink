// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package customreport

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestTimeSeriesScopeCRUDModel(t *testing.T) {
	mocker.InitTestDBConfig("../../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	suffix := time.Now().UnixNano()
	groupID := uint(700000 + suffix%100000)
	scopeName := fmt.Sprintf("scope_%d", suffix)

	scope := TimeSeriesScope{
		GroupID:   groupID,
		ScopeName: scopeName,
	}
	db.Delete(&TimeSeriesScope{}, "group_id = ? AND scope_name = ?", groupID, scopeName)

	err := scope.Create(db)
	require.NoError(t, err)

	var created TimeSeriesScope
	err = NewTimeSeriesScopeQuerySet(db).GroupIDEq(groupID).ScopeNameEq(scopeName).One(&created)
	require.NoError(t, err)
	assert.NotZero(t, created.LastModifyTime)

	created.DimensionConfig = `{"dimensions":["target","module"]}`
	created.AutoRules = `["^cpu_.*"]`
	created.CreateFrom = "default"
	err = created.Update(
		db,
		TimeSeriesScopeDBSchema.DimensionConfig,
		TimeSeriesScopeDBSchema.AutoRules,
		TimeSeriesScopeDBSchema.CreateFrom,
	)
	require.NoError(t, err)

	var updated TimeSeriesScope
	err = NewTimeSeriesScopeQuerySet(db).GroupIDEq(groupID).ScopeNameEq(scopeName).One(&updated)
	require.NoError(t, err)
	assert.JSONEq(t, `{"dimensions":["target","module"]}`, updated.DimensionConfig)
	assert.JSONEq(t, `["^cpu_.*"]`, updated.AutoRules)
	assert.Equal(t, "default", updated.CreateFrom)

	err = updated.Delete(db)
	require.NoError(t, err)

	var deleted TimeSeriesScope
	err = NewTimeSeriesScopeQuerySet(db).GroupIDEq(groupID).ScopeNameEq(scopeName).One(&deleted)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestTimeSeriesScopeBatchCRUDModel(t *testing.T) {
	mocker.InitTestDBConfig("../../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	suffix := time.Now().UnixNano()
	groupID := uint(770000 + suffix%100000)
	scopeNames := []string{
		fmt.Sprintf("batch_scope_%d_1", suffix),
		fmt.Sprintf("batch_scope_%d_2", suffix),
		fmt.Sprintf("batch_scope_%d_3", suffix),
	}
	for _, name := range scopeNames {
		db.Delete(&TimeSeriesScope{}, "group_id = ? AND scope_name = ?", groupID, name)
	}

	scopes := []TimeSeriesScope{
		{GroupID: groupID, ScopeName: scopeNames[0]},
		{GroupID: groupID, ScopeName: scopeNames[1]},
		{GroupID: groupID, ScopeName: scopeNames[2]},
	}
	for i := range scopes {
		err := scopes[i].Create(db)
		require.NoError(t, err)
	}

	updateDB := db.Model(&TimeSeriesScope{}).
		Where("group_id = ? AND scope_name IN (?)", groupID, scopeNames).
		Updates(map[string]any{
			"dimension_config": `{"dimensions":["target","module"]}`,
			"auto_rules":       `["^cpu_.*"]`,
			"create_from":      "default",
		})
	require.NoError(t, updateDB.Error)
	assert.Equal(t, int64(3), updateDB.RowsAffected)

	var updated []TimeSeriesScope
	err := NewTimeSeriesScopeQuerySet(db).GroupIDEq(groupID).ScopeNameIn(scopeNames...).All(&updated)
	require.NoError(t, err)
	require.Len(t, updated, 3)
	for _, s := range updated {
		assert.JSONEq(t, `{"dimensions":["target","module"]}`, s.DimensionConfig)
		assert.JSONEq(t, `["^cpu_.*"]`, s.AutoRules)
		assert.Equal(t, "default", s.CreateFrom)
	}

	deleteDB := db.Where("group_id = ? AND scope_name IN (?)", groupID, scopeNames).Delete(&TimeSeriesScope{})
	require.NoError(t, deleteDB.Error)
	assert.Equal(t, int64(3), deleteDB.RowsAffected)
}
