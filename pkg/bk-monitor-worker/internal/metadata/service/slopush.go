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
	"fmt"
	"strconv"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
)

var (
	Now         int64
	MaxPageSize = 5000
	RangeDay    = []int{1, 7, 30, 180, 365}
	SloName     = []string{"volume", "error", "latency", "availability"}
)

// StrategyDetail 策略信息
type StrategyDetail struct {
	Interval int32
	Name     string
	BkBizID  int32
}

// Alert 告警信息
type Alert struct {
	BkBizID          int32  `json:"bk_biz_id"`
	BkBizName        string `json:"bk_biz_name"`
	StrategyID       int32  `json:"strategy_id"`
	StrategyName     string `json:"strategy_name"`
	FirstAnomalyTime int64  `json:"first_anomaly_time"`
	LatestTime       int64  `json:"latest_time"`
	EventID          string `json:"event_id"`
	Status           string `json:"status"`
}

// InitStraID 初始化
func InitStraID(bkBizId int, scene string, now int64) (map[string][]BkBizStrategy, map[int]map[string]map[string][]int64, map[int][]map[int64]struct{}, error) {
	Now = now

	trueSloName := make(map[string][]BkBizStrategy)
	totalAlertTimeBucket := make(map[int]map[string]map[string][]int64)
	totalSloTimeBucketDict := make(map[int][]map[int64]struct{})

	db := mysql.GetDBSession().DB
	prefix := "/slo/"
	for _, sloName := range SloName {
		allBkBizStrategies, err := QueryAndDeduplicateStrategies(db, prefix, scene, sloName, bkBizId)
		if err != nil {
			return nil, nil, nil, errors.Wrapf(err, "failed to query and deduplicate strategies for sloName [%s]", sloName)
		}
		if len(allBkBizStrategies) > 0 {
			trueSloName[sloName] = allBkBizStrategies
		}
	}

	for _, day := range RangeDay {
		totalAlertTimeBucket[day] = make(map[string]map[string][]int64)
		totalSloTimeBucketDict[day] = make([]map[int64]struct{}, 0)
	}
	return trueSloName, totalAlertTimeBucket, totalSloTimeBucketDict, nil
}

// FindAllBiz 找到符合标准的biz
func FindAllBiz() (map[int32][]string, error) {
	db := mysql.GetDBSession().DB
	// 标签前缀
	prefix := "/slo/"
	// 标签后缀
	suffixes := SloName //{"volume", "error", "latency", "availability"}

	// 寻找符合标签规范的全部策略。然后统计其上层全部业务
	allBizIds, err := QueryBizV2(db, prefix, suffixes)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to query business IDs")
	}
	return allBizIds, nil
}

func getStrategyAggInterval(strategyIDs []BkBizStrategy, allStrategyAggInterval map[int32]StrategyDetail) {
	// Filter new strategy IDs
	newStrategyIDs := []int32{}
	for _, id := range strategyIDs {
		if _, exists := allStrategyAggInterval[id.StrategyID]; !exists {
			newStrategyIDs = append(newStrategyIDs, id.StrategyID)
		}
	}
	if len(newStrategyIDs) == 0 {
		return
	}

	for _, strategy := range strategyIDs {
		allStrategyAggInterval[strategy.StrategyID] = StrategyDetail{
			Interval: strategy.Interval,
			Name:     strategy.Name,
			BkBizID:  strategy.BkBizID,
		}
	}
}

func extractStrategyIDs(strategies []BkBizStrategy) []int32 {
	var strategyIDs []int32
	for _, strategy := range strategies {
		strategyIDs = append(strategyIDs, strategy.StrategyID)
	}
	return strategyIDs
}

func getAllAlerts(startTime int64, strategyIDs []BkBizStrategy, allStrategyAggInterval map[int32]StrategyDetail, bkBizID int32) ([]Alert, error) {
	if len(strategyIDs) == 0 {
		return []Alert{}, nil
	}

	// 数据清洗，conditions
	strategyIDs_con := extractStrategyIDs(strategyIDs)
	getStrategyAggInterval(strategyIDs, allStrategyAggInterval)

	conditions := []map[string]any{
		{"key": "severity", "value": []int{1, 2, 3}},
		{"key": "strategy_id", "value": strategyIDs_con, "condition": "and"},
	}

	// 获取告警全量数据和总数
	total, alerts, err := getFatalAlerts(conditions, startTime, 1, 1, bkBizID)
	if err != nil {
		return []Alert{}, errors.Wrapf(err, "failed to get all alerts")
	}
	totalPages := total / MaxPageSize
	alertList := []Alert{}
	for page := 1; page <= totalPages+1; page++ {
		// 分页获取告警数据
		_, alerts, err = getFatalAlerts(conditions, startTime, page, MaxPageSize, bkBizID)
		if err != nil {
			return []Alert{}, errors.Wrapf(err, "failed to get page [%d] alerts", page)
		}
		alertList = append(alertList, alerts...)
	}
	return alertList, nil
}

func getFatalAlerts(conditions []map[string]any, startTime int64, page, pageSize int, bkBizID int32) (int, []Alert, error) {
	// 调用外部服务以获取告警数据
	alertData, err := apiservice.Monitor.SearchAlert(conditions, startTime, Now, page, pageSize, bkBizID)
	if err != nil {
		// 处理调用错误
		return 0, nil, errors.Wrapf(err, "failed to search alerts")
	}

	// 检查返回的数据是否为空或格式不正确
	if alertData == nil {
		return 0, nil, errors.Wrapf(err, "alert data is nil")
	}

	// 获取总数
	total := alertData.Total

	// 初始化告警列表
	alerts := make([]Alert, 0, len(alertData.Alerts))

	// 遍历告警数据并转换为内部结构
	for _, alertInfo := range alertData.Alerts {
		alerts = append(alerts, Alert{
			BkBizID:          alertInfo.BkBizID,
			BkBizName:        alertInfo.BkBizName,
			StrategyID:       alertInfo.StrategyID,
			StrategyName:     alertInfo.StrategyName,
			FirstAnomalyTime: alertInfo.FirstAnomalyTime,
			LatestTime:       alertInfo.LatestTime,
			EventID:          alertInfo.EventID,
			Status:           alertInfo.Status,
		})
	}

	// 返回结果和 nil 错误
	return total, alerts, nil
}

func addSloTimeIntoDict(day int, sloKey string, strategyID int32, beginTime, endTime int64, totalAlertTimeBucket map[int]map[string]map[string][]int64) {
	// 初始化告警时间桶
	if totalAlertTimeBucket[day][sloKey] == nil {
		totalAlertTimeBucket[day][sloKey] = make(map[string][]int64)
	}

	// 获取当前已存入的告警时间
	key := fmt.Sprintf("%d", strategyID)
	existingTimes := totalAlertTimeBucket[day][sloKey][key]
	timeMap := make(map[int64]bool)

	// Populate the time map with existing times
	for _, t := range existingTimes {
		timeMap[t] = true
	}

	// 存入新告警时间
	for t := beginTime; t < endTime; t++ {
		if !timeMap[t] {
			totalAlertTimeBucket[day][sloKey][fmt.Sprintf("%d", strategyID)] = append(totalAlertTimeBucket[day][sloKey][fmt.Sprintf("%d", strategyID)], t)
			timeMap[t] = true
		}
	}

	// 添加总告警数
	if _, exists := totalAlertTimeBucket[day][sloKey]["error_number"]; !exists {
		totalAlertTimeBucket[day][sloKey]["error_number"] = []int64{0}
	}
	totalAlertTimeBucket[day][sloKey]["error_number"][0]++
}

// GetAllAlertTime 获取告警事件
func GetAllAlertTime(totalAlertTimeBucket map[int]map[string]map[string][]int64, trueSloName map[string][]BkBizStrategy, bkBizID int32, scene string) map[int32]StrategyDetail {
	// 定义策略详细信息
	allStrategyAggInterval := make(map[int32]StrategyDetail)
	for day := range totalAlertTimeBucket {
		// 每天的告警数据

		// 定义起始时间，当前时间-时间周期（1，7，30，180，365）
		startTime := Now - int64(day*24*60*60)
		for sloName, sloStrategyList := range trueSloName {
			// 每个方法论进行获取数据

			// 获取当前起始时间、当前方法论名称下策略、当前bizid下的告警数据，同时数据放到 AllStrategyAggInterval
			alertList, _ := getAllAlerts(startTime, sloStrategyList, allStrategyAggInterval, bkBizID)
			sloKey := sloName + "_alert_time"
			if totalAlertTimeBucket[day][sloKey] == nil {
				// 若当前时间周期，方法论下时间数据为空则新建map
				totalAlertTimeBucket[day][sloKey] = make(map[string][]int64)
			}
			for _, strategyID := range sloStrategyList {
				// 若当前时间周期，方法论，策略下时间数据为空则新建数组
				if totalAlertTimeBucket[day][sloKey][fmt.Sprintf("%d", strategyID.StrategyID)] == nil {
					totalAlertTimeBucket[day][sloKey][fmt.Sprintf("%d", strategyID.StrategyID)] = []int64{}
				}
			}
			for _, alert := range alertList {
				// 获取当前的告警信息
				strategyID := alert.StrategyID
				// 若当前告警数据不属于查询的策略之中则跳过
				if _, exists := allStrategyAggInterval[strategyID]; !exists {
					continue
				}
				// 设置结束时间
				endTime := alert.LatestTime
				// 若结束时间为0则设置为当前时间
				if endTime == 0 {
					endTime = Now
				}
				// 判断latest_time和first_anomaly_time是否相同，如果相同则需要添加一个聚合周期时间。因为endtime是终止时间，alert.FirstAnomalyTime为起始时间。
				if endTime == alert.FirstAnomalyTime {
					endTime += int64(allStrategyAggInterval[strategyID].Interval)
				}
				// 往TotalAlertTimeBucket添加时间数据。时间周期，方法论名，策略，起始时间，终止时间，存储桶
				addSloTimeIntoDict(day, sloKey, strategyID, max(alert.FirstAnomalyTime, startTime), endTime, totalAlertTimeBucket)
				metricSlo := endTime - max(alert.FirstAnomalyTime, startTime)
				metrics.RecordSloErrorEventTimeInfo(float64(metricSlo), fmt.Sprintf("%d", bkBizID), strconv.Itoa(day), fmt.Sprintf("%d", strategyID), scene, alert.EventID, alert.Status)
			}
		}
	}
	return allStrategyAggInterval
}

// CalculateMetric 计算指标数据
func CalculateMetric(totalAlertTimeBucket map[int]map[string]map[string][]int64, trueSloName map[string][]BkBizStrategy, allStrategyAggInterval map[int32]StrategyDetail, totalSloTimeBucketDict map[int][]map[int64]struct{}, bkBizID int32, scene string) {
	// 遍历 TotalAlertTimeBucket 中的每一天
	for day := range totalAlertTimeBucket {
		dayTime := int64(day * 24 * 60 * 60)             // 将天数转换为秒数
		totalErrorNumber := 0                            // 初始化总错误次数
		totalErrorSecondsSet := make(map[int64]struct{}) // 初始化总错误秒数集合
		totalVelatSloTimeSet := make(map[int64]struct{}) // 初始化总可用性时间集合

		// 遍历 TrueSloName 中的每个 SLO 名称
		for preName := range trueSloName {
			sloName := preName + "_alert_time"                       // 生成 SLO 告警时间的键
			strategyAlertTimes := totalAlertTimeBucket[day][sloName] // 获取该键的告警时间

			// 获取错误次数，如果存在则删除该键
			var errorNumber int
			if val, exists := strategyAlertTimes["error_number"]; exists && len(val) > 0 {
				errorNumber = int(val[0])
				delete(strategyAlertTimes, "error_number")
			} else {
				errorNumber = 0
			}

			// 遍历策略的告警时间
			for strategyIDStr := range strategyAlertTimes {
				strategyID64, _ := strconv.ParseInt(strategyIDStr, 10, 32)
				strategyID := int32(strategyID64)
				if _, exists := allStrategyAggInterval[strategyID]; !exists {
					continue // 如果策略不存在于 AllStrategyAggInterval 中则跳过
				}
				errorData := strategyAlertTimes[strategyIDStr]
				for _, t := range errorData {
					totalErrorSecondsSet[t] = struct{}{} // 将错误时间点加入总错误秒数集合
				}
				metrics.RecordSloErrorTimeInfo(float64(len(errorData)), fmt.Sprintf("%d", allStrategyAggInterval[strategyID].BkBizID), fmt.Sprintf("%d", day), fmt.Sprintf("%d", strategyID), allStrategyAggInterval[strategyID].Name, preName, scene)
			}

			velatSloTime := make(map[int64]struct{})
			if len(strategyAlertTimes) > 0 {
				// 遍历所有策略的告警时间并加入可用性时间集合
				for _, times := range strategyAlertTimes {
					for _, t := range times {
						velatSloTime[t] = struct{}{}
					}
				}
				totalSloTimeBucketDict[day] = append(totalSloTimeBucketDict[day], velatSloTime)
				value := (float64(dayTime-int64(len(velatSloTime))) / float64(dayTime)) * 100
				totalVelatSloTimeSet = mergeSets(totalVelatSloTimeSet, velatSloTime)
				metrics.RecordSloInfo(value, fmt.Sprintf("%d", bkBizID), fmt.Sprintf("%d", day), preName, scene)
			} else {
				metrics.RecordSloInfo(100.0, fmt.Sprintf("%d", bkBizID), fmt.Sprintf("%d", day), preName, scene)
			}
			totalErrorNumber += errorNumber
		}

		// 计算 MTTR 和 MTBF
		var mttrVal, mtbfVal float64
		if totalErrorNumber == 0 {
			mttrVal = 0
			mtbfVal = 0
		} else {
			mttrVal = float64(len(totalErrorSecondsSet)) / float64(totalErrorNumber)                // 平均修复时间
			mtbfVal = float64(dayTime-int64(len(totalVelatSloTimeSet))) / float64(totalErrorNumber) // 平均故障间隔时间
		}
		metrics.RecordMttr(mttrVal, fmt.Sprintf("%d", bkBizID), fmt.Sprintf("%d", day), scene)
		metrics.RecordMtbf(mtbfVal, fmt.Sprintf("%d", bkBizID), fmt.Sprintf("%d", day), scene)

		// 记录 SLO 错误时间和 SLO 值
		totalTimeLen := 0
		if len(totalSloTimeBucketDict[day]) > 0 {
			for _, set := range totalSloTimeBucketDict[day] {
				totalTimeLen += len(set)
			}
		}
		metrics.RecordSloErrorTime(float64(totalTimeLen), fmt.Sprintf("%d", bkBizID), fmt.Sprintf("%d", day), scene)
		value := (float64(dayTime-int64(totalTimeLen)) / float64(dayTime)) * 100
		metrics.RecordSlo(value, fmt.Sprintf("%d", bkBizID), fmt.Sprintf("%d", day), scene)
	}
}

func mergeSets(set1, set2 map[int64]struct{}) map[int64]struct{} {
	for k := range set2 {
		set1[k] = struct{}{}
	}
	return set1
}
