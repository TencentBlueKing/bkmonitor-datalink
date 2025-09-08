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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/slo"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestQueryBiz(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	// 标签前缀
	prefix := "/slo/"
	// 标签后缀
	suffixes := []string{"volume_test", "error_test", "latency_test", "availability_test"}
	labels := slo.AlarmStrategyLabel{
		LabelName:  "/slo/场景1/volume_test/",
		StrategyID: 99,
		BkBizID:    5000140,
	}
	db.Delete(&labels, "strategy_id = ?", 99)
	err := labels.Create(db)
	assert.NoError(t, err)

	alarmStrategy := slo.AlarmStrategyV2{
		ID:               99,
		Name:             "Test Strategy",
		BkBizID:          5000140,
		Source:           "test_source",
		Scenario:         "test_scenario",
		Type:             "test_type",
		IsEnabled:        true,
		CreateUser:       "test_user",
		CreateTime:       time.Now(),
		UpdateUser:       "test_user",
		UpdateTime:       time.Now(),
		IsInvalid:        false,
		InvalidType:      "none",
		App:              "test_app",
		Hash:             "test_hash",
		Path:             "/test/path",
		Snippet:          "test_snippet",
		Priority:         1,
		PriorityGroupKey: "test_key",
	}
	db.Delete(&alarmStrategy, "id = ?", 99)
	err2 := alarmStrategy.Create(db)
	assert.NoError(t, err2)

	// 创建一个 map，键为 int 类型，值为 []string 类型
	alarmMap := make(map[int32][]string)

	// 初始化键 5000140 对应的值为 [“场景1”]
	alarmMap[5000140] = []string{"场景1"}

	// 寻找符合标签规范的全部策略。然后统计其上层全部业务
	allBizIds, err := QueryBizV2(db, prefix, suffixes)
	assert.NoError(t, err)
	assert.Equal(t, alarmMap, allBizIds)
}

func TestQueryAndDeduplicateStrategies(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	// 标签前缀
	prefix := "/slo/"
	// 场景
	scene := "场景1"
	// 标签后缀
	sloName := "volume_test"
	// 业务id
	bkBizId := 5000140

	labels := slo.AlarmStrategyLabel{
		LabelName:  "/slo/场景1/volume_test/",
		StrategyID: 99,
		BkBizID:    5000140,
	}
	db.Delete(&labels, "strategy_id = ?", 99)
	err := labels.Create(db)
	assert.NoError(t, err)

	alarmStrategy := slo.AlarmStrategyV2{
		ID:               99,
		Name:             "Test Strategy",
		BkBizID:          5000140,
		Source:           "test_source",
		Scenario:         "test_scenario",
		Type:             "test_type",
		IsEnabled:        true,
		CreateUser:       "test_user",
		CreateTime:       time.Now(),
		UpdateUser:       "test_user",
		UpdateTime:       time.Now(),
		IsInvalid:        false,
		InvalidType:      "none",
		App:              "test_app",
		Hash:             "test_hash",
		Path:             "/test/path",
		Snippet:          "test_snippet",
		Priority:         1,
		PriorityGroupKey: "test_key",
	}
	db.Delete(&alarmStrategy, "id = ?", 99)
	err2 := alarmStrategy.Create(db)
	assert.NoError(t, err2)

	strategy := BkBizStrategy{
		Middle:     "场景1",
		BkBizID:    5000140,
		StrategyID: 99,
		Name:       "Test Strategy",
		Interval:   0,
	}
	strategies := []BkBizStrategy{strategy}

	allBkBizStrategies, err := QueryAndDeduplicateStrategies(db, prefix, scene, sloName, bkBizId)
	assert.NoError(t, err)
	assert.Equal(t, strategies, allBkBizStrategies)
}
