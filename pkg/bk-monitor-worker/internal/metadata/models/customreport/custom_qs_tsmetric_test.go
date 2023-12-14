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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestTimeSeriesMetric_CustomUpdate(t *testing.T) {
	config.FilePath = "../../../../bmw.yaml"
	mocker.PatchDBSession()
	db := mysql.GetDBSession().DB
	tsm := TimeSeriesMetric{
		TableID:        "table_id_test",
		FieldName:      "field_name_test",
		TagList:        "[]",
		LastModifyTime: time.Now(),
		LastIndex:      0,
		Label:          "123",
	}
	db.Delete(&tsm, "group_id = ?", 0)
	err := tsm.Create(db)
	assert.NoError(t, err)
	tsm.TableID = "table_id_test_new"
	tsm.FieldName = "field_name_test_new"
	tsm.Label = "new_label"
	err = tsm.CustomUpdate(db, TimeSeriesMetricDBSchema.TableID, TimeSeriesMetricDBSchema.FieldName)
	assert.NoError(t, err)
	var newTsm TimeSeriesMetric
	err = NewTimeSeriesMetricQuerySet(db).TableIDEq("table_id_test_new").One(&newTsm)
	assert.NoError(t, err)
	assert.Equal(t, tsm.TableID, newTsm.TableID)
	assert.Equal(t, tsm.FieldName, newTsm.FieldName)
	assert.Equal(t, "123", newTsm.Label)
}
