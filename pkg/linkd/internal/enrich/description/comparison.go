// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package description

import (
	"encoding/json"
	"fmt"
	"strings"
)

// renderComparisonConditions 描述发布时的历史比较条件。alarmd 标准事件没有
// 比较值或命中方向，因此不重算历史、不选择某一方向，也不宣称每个条件已命中。
func renderComparisonConditions(u unitDefinition, a Algorithm) (string, error) {
	var config struct {
		Floor         *float64 `json:"floor"`
		Ceil          *float64 `json:"ceil"`
		FloorInterval int      `json:"floor_interval"`
		CeilInterval  int      `json:"ceil_interval"`
		FetchType     string   `json:"fetch_type"`
		Days          int      `json:"days"`
		Method        string   `json:"method"`
		Ratio         *float64 `json:"ratio"`
		Shock         *float64 `json:"shock"`
		Threshold     *float64 `json:"threshold"`
	}
	if json.Unmarshal(a.Config, &config) != nil {
		return "", invalid("comparison_config_invalid")
	}
	var conditions []string
	switch a.Type {
	case "SimpleRingRatio", "SimpleYearRound", "AdvancedRingRatio", "AdvancedYearRound":
		for _, side := range []struct {
			percent  *float64
			interval int
			verb     string
		}{{config.Floor, config.FloorInterval, "下降"}, {config.Ceil, config.CeilInterval, "上升"}} {
			if side.percent == nil || *side.percent <= 0 {
				continue
			}
			comparison := "较前一时刻"
			switch a.Type {
			case "SimpleYearRound":
				comparison = "较上周同一时刻"
			case "AdvancedRingRatio", "AdvancedYearRound":
				if side.interval <= 0 {
					continue
				}
				fetch := "均值"
				if config.FetchType == "last" {
					fetch = "瞬间值"
				} else if config.FetchType != "" && config.FetchType != "avg" {
					return "", invalid("comparison_config_invalid")
				}
				comparison = fmt.Sprintf("较前%d个时间点的%s", side.interval, fetch)
				if a.Type == "AdvancedYearRound" {
					comparison = fmt.Sprintf("较前%d天内同一时刻绝对值的%s", side.interval, fetch)
				}
			}
			conditions = append(conditions, comparison+side.verb+"达到"+pythonNumber(*side.percent, false)+"%")
		}
	case "RingRatioAmplitude", "YearRoundAmplitude", "YearRoundRange":
		if config.Ratio == nil || config.Shock == nil {
			return "", invalid("comparison_config_invalid")
		}
		ratio := pythonNumber(*config.Ratio, false)
		shock := pythonNumber(*config.Shock, false) + u.thresholdSuffix(a.UnitPrefix)
		if a.Type == "RingRatioAmplitude" {
			if config.Threshold == nil {
				return "", invalid("comparison_config_invalid")
			}
			threshold := pythonNumber(*config.Threshold, false) + u.thresholdSuffix(a.UnitPrefix)
			conditions = append(conditions, "当前值与前一时刻值均 >= "+threshold+"，两者差值的绝对值 >= 前一时刻值 × "+ratio+" + "+shock)
		} else {
			mark, _ := compare(0, 0, config.Method)
			if config.Days <= 0 || mark == "" {
				return "", invalid("comparison_config_invalid")
			}
			if a.Type == "YearRoundRange" {
				conditions = append(conditions, fmt.Sprintf("当前值的绝对值 %s 前%d天内任意一天同一时刻值的绝对值 × %s + %s", mark, config.Days, ratio, shock))
			} else {
				conditions = append(conditions, fmt.Sprintf("当前值与前一时刻值的差值绝对值 %s 前%d天内任意一天同一时刻差值的绝对值 × %s + %s", mark, config.Days, ratio, shock))
			}
		}
	}
	if len(conditions) == 0 {
		return "", invalid("comparison_config_invalid")
	}
	return " 检测条件：（" + strings.Join(conditions, "或") + "）", nil
}
