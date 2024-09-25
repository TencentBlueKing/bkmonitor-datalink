// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/tokenparser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type bucket struct {
	Val string
	Cnt int
}

func itoSecStr(val int) string {
	return strconv.FormatFloat(float64(val)/1000, 'f', -1, 64)
}

func toBuckets(bucketMap map[int32]int32, itoFunc func(int) string) []bucket {
	bucketValList := make([]int, 0, len(bucketMap))
	for val := range bucketMap {
		bucketValList = append(bucketValList, int(val))
	}
	sort.Ints(bucketValList)

	count := 0
	buckets := make([]bucket, 0, len(bucketMap)+1)
	for _, val := range bucketValList {
		cnt, _ := bucketMap[int32(val)]
		count += int(cnt)
		buckets = append(buckets, bucket{itoFunc(val), count})
	}
	inf := strconv.FormatFloat(math.Inf(+1), 'f', -1, 64)
	buckets = append(buckets, bucket{inf, count})
	return buckets
}

func toIntBuckets(bucketMap map[int32]int32) []bucket {
	return toBuckets(bucketMap, strconv.Itoa)
}

func toSecondBuckets(bucketMap map[int32]int32) []bucket {
	return toBuckets(bucketMap, itoSecStr)
}

// toBucketMap 将分布统计字符串（"0|0,50|1,100|5"）转为结构化数据
func toBucketMap(s string) map[int32]int32 {
	bucketMap := make(map[int32]int32)
	pairs := strings.Split(s, ",")
	for _, pair := range pairs {
		// 按竖线分割每个键值对
		p := strings.Split(pair, "|")
		if len(p) != 2 {
			continue
		}
		val, err := strconv.Atoi(p[0])
		if err != nil {
			continue
		}
		cnt, err := strconv.Atoi(p[1])
		if err != nil {
			continue
		}
		bucketMap[int32(val)] = int32(cnt)
	}
	return bucketMap
}

// toHistogram 根据分布情况，生成统计指标
func toHistogram(name, target string, timestamp int64, buckets []bucket, dims map[string]string) []*promMapper {
	pms := make([]*promMapper, 0, len(buckets)+1)
	for _, b := range buckets {
		dims := utils.CloneMap(dims)
		dims["le"] = b.Val
		pm := &promMapper{
			Metrics:    common.MapStr{name + "_bucket": b.Cnt},
			Target:     target,
			Timestamp:  timestamp,
			Dimensions: dims,
		}
		pms = append(pms, pm)
	}
	pms = append(pms, &promMapper{
		Metrics:    common.MapStr{name + "_count": buckets[len(buckets)-1].Cnt},
		Target:     target,
		Timestamp:  timestamp,
		Dimensions: utils.CloneMap(dims),
	})
	return pms
}

// TarsEvent is a struct that embeds CommonEvent.
type TarsEvent struct {
	define.CommonEvent
}

// RecordType returns the type of record.
func (e TarsEvent) RecordType() define.RecordType {
	return define.RecordTars
}

var TarsConverter EventConverter = tarsConverter{}

type tarsConverter struct{}

func (c tarsConverter) ToEvent(token define.Token, dataId int32, data common.MapStr) define.Event {
	return TarsEvent{define.NewCommonEvent(token, dataId, data)}
}

func (c tarsConverter) ToDataID(record *define.Record) int32 {
	return record.Token.MetricsDataId
}

func (c tarsConverter) Convert(record *define.Record, f define.GatherFunc) {
	var events []define.Event
	dataID := c.ToDataID(record)
	data := record.Data.(*define.TarsData)
	if data.Type == define.TarsPropertyType {
		events = c.handleProp(record.Token, dataID, record.RequestClient.IP, data)
	} else {
		events = c.handleStat(record.Token, dataID, record.RequestClient.IP, data)
	}
	if len(events) > 0 {
		f(events...)
	}
}

// handleStat 处理服务统计指标
func (c tarsConverter) handleStat(token define.Token, dataID int32, ip string, data *define.TarsData) []define.Event {
	var events []define.Event
	sd := data.Data.(*define.TarsStatData)
	role := "server"
	if sd.FromClient {
		role = "client"
	}
	for head, body := range sd.Stats {
		masterName, _ := tokenparser.FromString(head.MasterName)
		slaveName, _ := tokenparser.FromString(head.SlaveName)
		dims := map[string]string{
			"role":           role,
			"master_name":    masterName,
			"slave_name":     slaveName,
			"interface_name": head.InterfaceName,
			"master_ip":      head.MasterIp,
			"slave_ip":       head.SlaveIp,
			"slave_port":     strconv.Itoa(int(head.SlavePort)),
			"return_value":   strconv.Itoa(int(head.ReturnValue)),
			"slave_set_name": head.SlaveSetName,
			"slave_set_area": head.SlaveSetArea,
			"slave_set_id":   head.SlaveSetID,
			"tars_version":   head.TarsVersion,
		}
		pms := toHistogram("tars_request_duration_seconds", ip, data.Timestamp, toSecondBuckets(body.IntervalCount), dims)
		pms = append(pms, &promMapper{
			Metrics: common.MapStr{
				"tars_timeout_total":                body.TimeoutCount,
				"tars_requests_total":               body.Count,
				"tars_exceptions_total":             body.ExecCount,
				"tars_request_duration_seconds_max": float64(body.MaxRspTime) / 1000,
				"tars_request_duration_seconds_min": float64(body.MinRspTime) / 1000,
				"tars_request_duration_seconds_sum": float64(body.TotalRspTime) / 1000,
			},
			Target:     ip,
			Timestamp:  data.Timestamp,
			Dimensions: utils.CloneMap(dims),
		})
		for _, pm := range pms {
			events = append(events, c.ToEvent(token, dataID, pm.AsMapStr()))
		}
	}
	return events
}

// handleStat 处理业务特性指标
func (c tarsConverter) handleProp(token define.Token, dataID int32, ip string, data *define.TarsData) []define.Event {
	events := make([]define.Event, 0)
	props := data.Data.(*define.TarsPropertyData).Props
	for head, body := range props {
		moduleName, _ := tokenparser.FromString(head.ModuleName)
		originDims := map[string]string{
			"ip":             head.Ip,
			"module_name":    moduleName,
			"property_name":  head.PropertyName,
			"set_name":       head.SetName,
			"set_area":       head.SetArea,
			"s_container":    head.SContainer,
			"i_property_ver": strconv.Itoa(int(head.IPropertyVer)),
		}

		for _, info := range body.VInfo {
			dims := utils.CloneMap(originDims)
			// 补充统计类型作为维度
			dims["property_policy"] = info.Policy
			metricName := "tars_property_" + strings.ToLower(info.Policy)

			switch info.Policy {
			case "Distr":
				bucketMap := toBucketMap(info.Value)
				if len(bucketMap) == 0 {
					logger.Warnf(
						"[handleProp] empty distrMap, dataID=%d, ip=%v, propertyName=%s, Distr=%s",
						dataID, ip, head.PropertyName, info.Value)
					continue
				}
				pms := toHistogram(metricName, ip, data.Timestamp, toIntBuckets(bucketMap), dims)
				for _, pm := range pms {
					events = append(events, c.ToEvent(token, dataID, pm.AsMapStr()))
				}
			default:
				// Policy -> Max / Min / Avg / Sum / Count
				// PropertyName 可在服务运行中自定义，属于变化维度，不适合当 MetricName
				val, err := strconv.ParseFloat(info.Value, 64)
				if err != nil {
					DefaultMetricMonitor.IncConverterFailedCounter(define.RecordTars, dataID)
					continue
				}

				pm := &promMapper{
					Metrics:    common.MapStr{metricName: val},
					Target:     ip,
					Timestamp:  data.Timestamp,
					Dimensions: dims,
				}
				events = append(events, c.ToEvent(token, dataID, pm.AsMapStr()))
			}
		}
	}
	return events
}
