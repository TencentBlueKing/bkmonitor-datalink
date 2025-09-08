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
	"fmt"

	"github.com/jinzhu/gorm"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/slo"
)

// 导入内置 fmt

// BkBizStrategy 定义只存储 BkBizID 和 StrategyID 的结构体
type BkBizStrategy struct {
	Middle     string
	BkBizID    int32
	StrategyID int32
	Name       string
	Interval   int32
}

// Config 配置信息
type Config struct {
	AggInterv int32 `json:"agg_interval"`
}

// QueryAndDeduplicateStrategies 查询并去重的方法
func QueryAndDeduplicateStrategies(db *gorm.DB, prefix string, middleParts string, suffixes string, bkBizId int) ([]BkBizStrategy, error) {
	var allBkBizStrategies []BkBizStrategy

	pattern := fmt.Sprintf("%s%s/%s/", prefix, middleParts, suffixes)
	var results []struct {
		Middle     string
		BkBizID    int32
		StrategyID int32
		Name       string
	}

	res := db.Table("alarm_strategy_label AS label").
		Select("SUBSTRING_INDEX(SUBSTRING_INDEX(label.label_name, '/', 3), '/', -1) AS middle, label.bk_biz_id, label.strategy_id, strategy.name").
		Joins("INNER JOIN alarm_strategy_v2 AS strategy ON label.strategy_id = strategy.id").
		Where("label.bk_biz_id = ? AND label.strategy_id != ? AND label.label_name = ?", bkBizId, 0, pattern).
		Find(&results)

	if res.Error != nil {
		return nil, fmt.Errorf("failed to query data: %v", res.Error)
	}

	for _, result := range results {
		var config slo.AlarmQueryConfigV2
		res := db.Where("strategy_id = ?", result.StrategyID).First(&config)
		if res.Error != nil && res.Error != gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("failed to query data: %v", res.Error)
		}

		var interval int32
		if res.Error == nil {
			var jsonConfig Config
			if err := json.Unmarshal([]byte(config.Config), &jsonConfig); err != nil {
				return nil, fmt.Errorf("failed to unmarshal config: %v", err)
			}
			interval = jsonConfig.AggInterv
		}
		allBkBizStrategies = append(allBkBizStrategies, BkBizStrategy{
			BkBizID:    result.BkBizID,
			StrategyID: result.StrategyID,
			Name:       result.Name,
			Interval:   interval,
			Middle:     result.Middle,
		})
	}

	return allBkBizStrategies, nil
}

// Result 结果
type Result struct {
	BkBizID int32
	Middle  string
}

// QueryBizV2 全量检索业务
func QueryBizV2(db *gorm.DB, prefix string, suffixes []string) (map[int32][]string, error) {
	// 检索biz和场景
	bkBizIDToMiddleMap := make(map[int32]map[string]struct{})

	for _, suffix := range suffixes {
		// 定义标签 /slo/场景名称/后缀
		pattern := fmt.Sprintf("%s%%/%s/", prefix, suffix)
		var results []Result

		// 检索alarm_strategy_label表
		// bk_biz_id 和 strategy_id 不为0的，符合/slo/场景名称/后缀，且在alarm_strategy_v2中存在的策略
		// 输出策略所属的bk_biz_id 和 场景名称（标签中间的部分）
		res := db.Table("alarm_strategy_label AS label").
			Select("label.bk_biz_id, SUBSTRING_INDEX(SUBSTRING_INDEX(label.label_name, '/', 3), '/', -1) AS middle").
			Joins("INNER JOIN alarm_strategy_v2 AS strategy ON label.strategy_id = strategy.id").
			Where("label.bk_biz_id != ? AND label.strategy_id != ? AND label.label_name LIKE ?", 0, 0, pattern).
			Find(&results)

		if res.Error != nil {
			return nil, fmt.Errorf("failed to query data: %v", res.Error)
		}

		for _, result := range results {
			if _, exists := bkBizIDToMiddleMap[result.BkBizID]; !exists {
				bkBizIDToMiddleMap[result.BkBizID] = make(map[string]struct{})
			}
			bkBizIDToMiddleMap[result.BkBizID][result.Middle] = struct{}{}
		}
	}

	// 将 map 转换为需要的格式
	finalResult := make(map[int32][]string)
	for bkBizID, middleSet := range bkBizIDToMiddleMap {
		var middleList []string
		for middle := range middleSet {
			middleList = append(middleList, middle)
		}
		finalResult[bkBizID] = middleList
	}

	return finalResult, nil
}
