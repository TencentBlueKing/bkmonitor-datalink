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
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
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

	TrueSloName := make(map[string][]BkBizStrategy)
	TotalAlertTimeBucket := make(map[int]map[string]map[string][]int64)
	TotalSloTimeBucketDict := make(map[int][]map[int64]struct{})

	db := mysql.GetDBSession().DB
	prefix := "/slo/"
	for _, sloName := range SloName {
		allBkBizStrategies, err := QueryAndDeduplicateStrategies(db, prefix, scene, sloName, bkBizId)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to query and deduplicate strategies for sloName %s: %w", sloName, err)
		}
		if len(allBkBizStrategies) > 0 {
			TrueSloName[sloName] = allBkBizStrategies
		}
	}
	//logger.Info("Velta and strategy:", TrueSloName)

	for _, day := range RangeDay {
		TotalAlertTimeBucket[day] = make(map[string]map[string][]int64)
		TotalSloTimeBucketDict[day] = make([]map[int64]struct{}, 0)
	}
	return TrueSloName, TotalAlertTimeBucket, TotalSloTimeBucketDict, nil
}

// FindAllBiz 找到符合标准的biz
func FindAllBiz() (map[int32][]string, error) {
	db := mysql.GetDBSession().DB
	//标签前缀
	prefix := "/slo/"
	//标签后缀
	suffixes := SloName //{"volume", "error", "latency", "availability"}

	// 寻找符合标签规范的全部策略。然后统计其上层全部业务
	allBizIds, err := QueryBizV2(db, prefix, suffixes)
	if err != nil {
		return nil, fmt.Errorf("failed to query business IDs: %w", err)
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

func getAllAlerts(startTime int64, strategyIDs []BkBizStrategy, AllStrategyAggInterval map[int32]StrategyDetail, BkBizID int32) ([]Alert, error) {
	if len(strategyIDs) == 0 {
		return []Alert{}, nil
	}

	// 数据清洗，conditions
	strategyIDs_con := extractStrategyIDs(strategyIDs)
	getStrategyAggInterval(strategyIDs, AllStrategyAggInterval)

	conditions := []map[string]interface{}{
		{"key": "severity", "value": []int{1, 2, 3}},
		{"key": "strategy_id", "value": strategyIDs_con, "condition": "and"},
	}

	// 获取告警全量数据和总数
	total, alerts, err := getFatalAlerts(conditions, startTime, 1, 1, BkBizID)
	if err != nil {
		logger.Errorf("getFatalAlerts failed: %v", err)
		return []Alert{}, err
	}
	totalPages := total / MaxPageSize
	alertList := []Alert{}
	for page := 1; page <= totalPages+1; page++ {
		// 分页获取告警数据
		_, alerts, err = getFatalAlerts(conditions, startTime, page, MaxPageSize, BkBizID)
		if err != nil {
			logger.Errorf("getFatalAlerts failed: %v", err)
			return []Alert{}, err
		}
		alertList = append(alertList, alerts...)
	}
	return alertList, nil
}

func getFatalAlerts(conditions []map[string]interface{}, startTime int64, page, pageSize int, BkBizID int32) (int, []Alert, error) {
	url := "https://bkmonitorv3.apigw.o.woa.com/prod/search_alert/"
	payload := map[string]interface{}{
		"bk_app_code":   config.BkApiAppCode,
		"bk_app_secret": config.BkApiAppSecret,
		"bk_biz_ids":    []int{int(BkBizID)},
		"start_time":    startTime,
		"end_time":      Now,
		"page":          page,
		"page_size":     pageSize,
		"conditions":    conditions,
	}
	body, _ := json.Marshal(payload)

	// 创建请求
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		logger.Error("Error creating request:", err)
		return 0, []Alert{}, err
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	authHeader := map[string]string{
		"bk_app_code":   config.BkApiAppCode,
		"bk_app_secret": config.BkApiAppSecret,
	}
	authHeaderJson, _ := json.Marshal(authHeader)
	req.Header.Set("X-Bkapi-Authorization", string(authHeaderJson))

	// 发送请求
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("Error querying alerts:", err)
		return 0, []Alert{}, err
	}
	defer resp.Body.Close()

	data, _ := ioutil.ReadAll(resp.Body)
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		logger.Error("Error unmarshalling alerts:", err)
		return 0, []Alert{}, err
	}
	if result["data"] == nil {
		logger.Error("Error finding data in alerts :", conditions, " startTime:", startTime, " BkBizID:", BkBizID)
		return 0, []Alert{}, err
	}

	alertData := result["data"].(map[string]interface{})
	total := int(alertData["total"].(float64))
	alerts := []Alert{}
	for _, alert := range alertData["alerts"].([]interface{}) {
		alertMap := alert.(map[string]interface{})
		alerts = append(alerts, Alert{
			BkBizID:          int32(alertMap["bk_biz_id"].(float64)),
			BkBizName:        alertMap["bk_biz_name"].(string),
			StrategyID:       int32(alertMap["strategy_id"].(float64)),
			StrategyName:     alertMap["strategy_name"].(string),
			FirstAnomalyTime: int64(alertMap["first_anomaly_time"].(float64)),
			LatestTime:       int64(alertMap["latest_time"].(float64)),
			EventID:          alertMap["event_id"].(string),
			Status:           alertMap["status"].(string),
		})
	}
	return total, alerts, nil
}

func addSloTimeIntoDict(day int, sloKey string, strategyID int32, beginTime, endTime int64, TotalAlertTimeBucket map[int]map[string]map[string][]int64) {
	// 初始化告警时间桶
	if TotalAlertTimeBucket[day][sloKey] == nil {
		TotalAlertTimeBucket[day][sloKey] = make(map[string][]int64)
	}

	// 获取当前已存入的告警时间
	key := fmt.Sprintf("%d", strategyID)
	existingTimes := TotalAlertTimeBucket[day][sloKey][key]
	timeMap := make(map[int64]bool)

	// Populate the time map with existing times
	for _, t := range existingTimes {
		timeMap[t] = true
	}

	// 存入新告警时间
	for t := beginTime; t < endTime; t++ {
		if !timeMap[t] {
			TotalAlertTimeBucket[day][sloKey][fmt.Sprintf("%d", strategyID)] = append(TotalAlertTimeBucket[day][sloKey][fmt.Sprintf("%d", strategyID)], t)
			timeMap[t] = true
		}
	}

	// 添加总告警数
	if _, exists := TotalAlertTimeBucket[day][sloKey]["error_number"]; !exists {
		TotalAlertTimeBucket[day][sloKey]["error_number"] = []int64{0}
	}
	TotalAlertTimeBucket[day][sloKey]["error_number"][0]++
}

// GetAllAlertTime 获取告警事件
func GetAllAlertTime(TotalAlertTimeBucket map[int]map[string]map[string][]int64, TrueSloName map[string][]BkBizStrategy, BkBizID int32) map[int32]StrategyDetail {
	//定义策略详细信息
	AllStrategyAggInterval := make(map[int32]StrategyDetail)
	for day := range TotalAlertTimeBucket {
		//每天的告警数据

		//定义起始时间，当前时间-时间周期（1，7，30，180，365）
		startTime := Now - int64(day*24*60*60)
		for sloName, sloStrategyList := range TrueSloName {
			// 每个方法论进行获取数据

			// 获取当前起始时间、当前方法论名称下策略、当前bizid下的告警数据，同时数据放到 AllStrategyAggInterval
			alertList, _ := getAllAlerts(startTime, sloStrategyList, AllStrategyAggInterval, BkBizID)
			sloKey := sloName + "_alert_time"
			if TotalAlertTimeBucket[day][sloKey] == nil {
				// 若当前时间周期，方法论下时间数据为空则新建map
				TotalAlertTimeBucket[day][sloKey] = make(map[string][]int64)
			}
			for _, strategyID := range sloStrategyList {
				// 若当前时间周期，方法论，策略下时间数据为空则新建数组
				if TotalAlertTimeBucket[day][sloKey][fmt.Sprintf("%d", strategyID.StrategyID)] == nil {
					TotalAlertTimeBucket[day][sloKey][fmt.Sprintf("%d", strategyID.StrategyID)] = []int64{}
				}
			}
			for _, alert := range alertList {
				// 获取当前的告警信息
				strategyID := alert.StrategyID
				// 若当前告警数据不属于查询的策略之中则跳过
				if _, exists := AllStrategyAggInterval[strategyID]; !exists {
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
					endTime += int64(AllStrategyAggInterval[strategyID].Interval)
				}
				// 往TotalAlertTimeBucket添加时间数据。时间周期，方法论名，策略，起始时间，终止时间，存储桶
				addSloTimeIntoDict(day, sloKey, strategyID, maxForSlo(alert.FirstAnomalyTime, startTime), endTime, TotalAlertTimeBucket)
			}
		}
	}
	return AllStrategyAggInterval
}

// CalculateMetric 计算指标数据
func CalculateMetric(TotalAlertTimeBucket map[int]map[string]map[string][]int64, TrueSloName map[string][]BkBizStrategy, AllStrategyAggInterval map[int32]StrategyDetail, TotalSloTimeBucketDict map[int][]map[int64]struct{}, BkBizID int32, scene string) {
	// 遍历 TotalAlertTimeBucket 中的每一天
	for day := range TotalAlertTimeBucket {
		dayTime := int64(day * 24 * 60 * 60)             // 将天数转换为秒数
		totalErrorNumber := 0                            // 初始化总错误次数
		totalErrorSecondsSet := make(map[int64]struct{}) // 初始化总错误秒数集合
		totalVelatSloTimeSet := make(map[int64]struct{}) // 初始化总可用性时间集合

		// 遍历 TrueSloName 中的每个 SLO 名称
		for preName := range TrueSloName {
			sloName := preName + "_alert_time"                       // 生成 SLO 告警时间的键
			strategyAlertTimes := TotalAlertTimeBucket[day][sloName] // 获取该键的告警时间

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
				if _, exists := AllStrategyAggInterval[strategyID]; !exists {
					continue // 如果策略不存在于 AllStrategyAggInterval 中则跳过
				}
				errorData := strategyAlertTimes[strategyIDStr]
				for _, t := range errorData {
					totalErrorSecondsSet[t] = struct{}{} // 将错误时间点加入总错误秒数集合
				}
				metrics.RecordSloErrorTimeInfo(float64(len(errorData)), fmt.Sprintf("%d", AllStrategyAggInterval[strategyID].BkBizID), fmt.Sprintf("%d", day), fmt.Sprintf("%d", strategyID), AllStrategyAggInterval[strategyID].Name, preName, scene)
			}

			velatSloTime := make(map[int64]struct{})
			if len(strategyAlertTimes) > 0 {
				// 遍历所有策略的告警时间并加入可用性时间集合
				for _, times := range strategyAlertTimes {
					for _, t := range times {
						velatSloTime[t] = struct{}{}
					}
				}
				TotalSloTimeBucketDict[day] = append(TotalSloTimeBucketDict[day], velatSloTime)
				value := getDivisionVal(int64(len(velatSloTime)), dayTime)
				totalVelatSloTimeSet = mergeSets(totalVelatSloTimeSet, velatSloTime)
				metrics.RecordSloInfo(value, fmt.Sprintf("%d", BkBizID), fmt.Sprintf("%d", day), preName, scene)
			} else {
				metrics.RecordSloInfo(100.0, fmt.Sprintf("%d", BkBizID), fmt.Sprintf("%d", day), preName, scene)
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
		metrics.RecordMttr(mttrVal, fmt.Sprintf("%d", BkBizID), fmt.Sprintf("%d", day), scene)
		metrics.RecordMtbf(mtbfVal, fmt.Sprintf("%d", BkBizID), fmt.Sprintf("%d", day), scene)

		// 记录 SLO 错误时间和 SLO 值
		totalTimeLen := 0
		if len(TotalSloTimeBucketDict[day]) > 0 {
			for _, set := range TotalSloTimeBucketDict[day] {
				totalTimeLen += len(set)
			}
		}
		metrics.RecordSloErrorTime(float64(totalTimeLen), fmt.Sprintf("%d", BkBizID), fmt.Sprintf("%d", day), scene)
		value := getDivisionVal(int64(totalTimeLen), dayTime)
		metrics.RecordSlo(value, fmt.Sprintf("%d", BkBizID), fmt.Sprintf("%d", day), scene)
	}
}

func mergeSets(set1, set2 map[int64]struct{}) map[int64]struct{} {
	for k := range set2 {
		set1[k] = struct{}{}
	}
	return set1
}

func getDivisionVal(velatSloTime, dayTime int64) float64 {
	// 使用 big.Float 进行精确计算
	dayTimeBig := new(big.Float).SetInt64(dayTime)
	velatSloTimeBig := new(big.Float).SetInt64(velatSloTime)
	diff := new(big.Float).Sub(dayTimeBig, velatSloTimeBig)
	value := new(big.Float).Quo(diff, dayTimeBig)
	value.Mul(value, big.NewFloat(100))

	// 格式化为两位小数精度的浮点数
	float2Value, _ := value.Float64()
	float2Value = roundToTwoDecimalPlaces(float2Value)

	// 如果百分比恰好为100.0，并且集合不为空，则调整为99.99
	if float2Value == 100.0 && velatSloTime > 0 {
		float2Value = 99.99
	}
	return float2Value
}

// roundToTwoDecimalPlaces 将浮点数四舍五入到两位小数
func roundToTwoDecimalPlaces(f float64) float64 {
	value := new(big.Float).SetFloat64(f)
	value.SetMode(big.ToZero)
	value.SetPrec(64)
	value.Quo(value, big.NewFloat(1))
	value.Mul(value, big.NewFloat(100))
	value.SetPrec(64)
	value.Quo(value, big.NewFloat(100))
	float2Value, _ := value.Float64()
	return float2Value
}

func maxForSlo(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
