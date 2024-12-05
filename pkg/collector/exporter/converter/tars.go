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
	"fmt"
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

const (
	resourceTagsScopeName     = "scope_name"
	resourceTagsRPCSystem     = "rpc_system"
	resourceTagsServiceName   = "service_name"
	resourceTagsInstance      = "instance"
	resourceTagsContainerName = "container_name"
	resourceTagsConSetid      = "con_setid"
	resourceTagsVersion       = "version"
)

const (
	rpcMetricTagsCallerServer   = "caller_server"
	rpcMetricTagsCallerIp       = "caller_ip"
	rpcMetricTagsCalleeServer   = "callee_server"
	rpcMetricTagsCalleeMethod   = "callee_method"
	rpcMetricTagsCalleeIp       = "callee_ip"
	rpcMetricTagsCalleeConSetid = "callee_con_setid"
	rpcMetricTagsCode           = "code"
	rpcMetricTagsCodeType       = "code_type"
	rpcMetricTagsUserExt1       = "user_ext1"
)

const (
	rpcMetricTagsCodeTypeSuccess   = "success"
	rpcMetricTagsCodeTypeException = "exception"
	rpcMetricTagsCodeTypeTimeout   = "timeout"
)

const (
	tarsStatTagsRole          = "role"
	tarsStatTagsMasterName    = "master_name"
	tarsStatTagsSlaveName     = "slave_name"
	tarsStatTagsInterfaceName = "interface_name"
	tarsStatTagsMasterIp      = "master_ip"
	tarsStatTagsSlaveIp       = "slave_ip"
	tarsStatTagsSlavePort     = "slave_port"
	tarsStatTagsReturnValue   = "return_value"
	tarsStatTagsSlaveSetName  = "slave_set_name"
	tarsStatTagsSlaveSetArea  = "slave_set_area"
	tarsStatTagsSlaveSetId    = "slave_set_id"
	tarsStatTagsTarsVersion   = "tars_version"
)

const (
	tarsStatTagsRoleClient = "client"
	tarsStatTagsRoleServer = "server"
)

const (
	tarsPropertyTagsIp             = "ip"
	tarsPropertyTagsModuleName     = "module_name"
	tarsPropertyTagsPropertyName   = "property_name"
	tarsPropertyTagsPropertyPolicy = "property_policy"
	tarsPropertyTagsSetName        = "set_name"
	tarsPropertyTagsSetArea        = "set_area"
	tarsPropertyTagsSetId          = "set_id"
	tarsPropertyTagsSContainer     = "s_container"
	tarsPropertyTagsIPropertyVer   = "i_property_ver"
)

type bucket struct {
	Val string
	Cnt int
}

// splitAtLastOnce 根据指定 sep 从右往左切割 s 一次
func splitAtLastOnce(s, sep string) (string, string) {
	lastIndex := strings.LastIndex(s, sep)
	if lastIndex == -1 {
		return s, ""
	}
	return s[:lastIndex], s[lastIndex+1:]
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

// generateConSetid 生成 ConSetid
func generateConSetid(setName, setArea, setId string) string {
	return fmt.Sprintf("%s.%s.%s", setName, setArea, setId)
}

// statToRPCMetricDims 将 Tars Stat 维度转为通用 RPC 模调维度
func statToRPCMetricDims(src, attrs map[string]string) map[string]string {
	role, _ := src[tarsStatTagsRole]
	dst := utils.MergeMaps(attrs, map[string]string{
		resourceTagsRPCSystem: define.RequestTars.S(),
		resourceTagsScopeName: fmt.Sprintf("%s_metrics", role),
	})
	var slaveSetName, slaveSetArea, slaveSetId string
	for key, value := range src {
		switch key {
		case tarsStatTagsMasterName:
			callerServer, version := splitAtLastOnce(value, "@")
			dst[rpcMetricTagsCallerServer] = callerServer
			dst[resourceTagsVersion] = version
			// 主调场景，MasterName 是上报服务
			if role == tarsStatTagsRoleClient {
				dst[resourceTagsServiceName] = callerServer
			}
		case tarsStatTagsMasterIp:
			dst[rpcMetricTagsCallerIp] = value
			// 主调场景，MasterIp 是服务 IP
			if role == tarsStatTagsRoleClient {
				dst[resourceTagsInstance] = value
			}
		case tarsStatTagsSlaveName:
			dst[rpcMetricTagsCalleeServer] = value
			// 被调场景，SlaveName 是上报服务
			if role == tarsStatTagsRoleServer {
				dst[resourceTagsServiceName] = value
			}
		case tarsStatTagsSlaveIp:
			dst[rpcMetricTagsCalleeIp] = value
			// 被调场景，SlaveIp 是服务 IP
			if role == tarsStatTagsRoleServer {
				dst[resourceTagsInstance] = value
			}
		case tarsStatTagsSlavePort:
			dst[rpcMetricTagsUserExt1] = value
		case tarsStatTagsInterfaceName:
			dst[rpcMetricTagsCalleeMethod] = value
		case tarsStatTagsReturnValue:
			dst[rpcMetricTagsCode] = value
		case tarsStatTagsSlaveSetName:
			slaveSetName = value
		case tarsStatTagsSlaveSetArea:
			slaveSetArea = value
		case tarsStatTagsSlaveSetId:
			slaveSetId = value
		}
	}
	dst[rpcMetricTagsCalleeConSetid] = fmt.Sprintf("%s.%s.%s", slaveSetName, slaveSetArea, slaveSetId)
	return dst
}

// propToCustomMetricDims 将 Tars Property 维度转为自定义指标维度
func propToCustomMetricDims(src, attrs map[string]string) map[string]string {
	dst := utils.MergeMaps(attrs, map[string]string{
		resourceTagsRPCSystem: define.RequestTars.S(),
		resourceTagsScopeName: fmt.Sprintf("%s_property", define.RequestTars.S()),
	})
	var setName, setArea, setId string
	for key, value := range src {
		switch key {
		case tarsPropertyTagsIp:
			dst[resourceTagsInstance] = value
		case tarsPropertyTagsModuleName:
			dst[resourceTagsServiceName] = value
		case tarsPropertyTagsSContainer:
			dst[resourceTagsContainerName] = value
		case tarsPropertyTagsSetName:
			setName = value
		case tarsPropertyTagsSetArea:
			setArea = value
		case tarsPropertyTagsSetId:
			setId = value
		default:
			dst[key] = value
		}
	}
	dst[resourceTagsConSetid] = generateConSetid(setName, setArea, setId)
	return dst
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
	for head, body := range sd.Stats {
		masterName, _ := tokenparser.FromString(head.MasterName)
		slaveName, _ := tokenparser.FromString(head.SlaveName)
		dims := map[string]string{
			tarsStatTagsMasterName:    masterName,
			tarsStatTagsSlaveName:     slaveName,
			tarsStatTagsInterfaceName: head.InterfaceName,
			tarsStatTagsMasterIp:      head.MasterIp,
			tarsStatTagsSlaveIp:       head.SlaveIp,
			tarsStatTagsSlavePort:     strconv.Itoa(int(head.SlavePort)),
			tarsStatTagsReturnValue:   strconv.Itoa(int(head.ReturnValue)),
			tarsStatTagsSlaveSetName:  head.SlaveSetName,
			tarsStatTagsSlaveSetArea:  head.SlaveSetArea,
			tarsStatTagsSlaveSetId:    head.SlaveSetID,
			tarsStatTagsTarsVersion:   head.TarsVersion,
		}

		var role string
		if sd.FromClient {
			role = tarsStatTagsRoleClient
			// 主调场景上报指标缺少主调 IP 维度，使用上报 IP 填充
			if head.MasterIp == "" {
				dims[tarsStatTagsMasterIp] = ip
			}
		} else {
			role = tarsStatTagsRoleServer
			// 被调场景上报指标缺少被调 IP 维度，使用上报 IP 填充
			if head.SlaveIp == "" {
				dims[tarsStatTagsSlaveIp] = ip
			}
		}
		dims[tarsStatTagsRole] = role

		// 生成 Tars 指标
		pms := toHistogram("tars_request_duration_seconds", ip, data.Timestamp, toSecondBuckets(body.IntervalCount), dims)
		pms = append(pms, &promMapper{
			Metrics: common.MapStr{
				"tars_requests_total":               body.Count,
				"tars_exceptions_total":             body.ExecCount,
				"tars_timeout_total":                body.TimeoutCount,
				"tars_request_duration_seconds_max": float64(body.MaxRspTime) / 1000,
				"tars_request_duration_seconds_min": float64(body.MinRspTime) / 1000,
				"tars_request_duration_seconds_sum": float64(body.TotalRspTime) / 1000,
			},
			Target:     ip,
			Timestamp:  data.Timestamp,
			Dimensions: utils.CloneMap(dims),
		})

		// 生成 RPC 指标
		// Map 无序，借助列表有序生成指标，保证代码可测试性
		codeTypes := []string{rpcMetricTagsCodeTypeSuccess, rpcMetricTagsCodeTypeException, rpcMetricTagsCodeTypeTimeout}
		codeTypeReqCntMap := map[string]int32{
			rpcMetricTagsCodeTypeSuccess:   body.Count,
			rpcMetricTagsCodeTypeException: body.ExecCount,
			rpcMetricTagsCodeTypeTimeout:   body.TimeoutCount,
		}
		for _, codeType := range codeTypes {
			cnt, _ := codeTypeReqCntMap[codeType]
			pms = append(pms, &promMapper{
				Metrics:    common.MapStr{fmt.Sprintf("rpc_%s_handled_total", role): cnt},
				Target:     ip,
				Timestamp:  data.Timestamp,
				Dimensions: statToRPCMetricDims(dims, map[string]string{rpcMetricTagsCodeType: codeType}),
			})
		}

		// ReturnValue = 0 也可能是超时 or 异常，而协议的分桶数据不区分返回码状态，所以此处只能大致判断，写一个预估的返回码类型
		codeType := rpcMetricTagsCodeTypeSuccess
		switch {
		case body.TimeoutCount > 0:
			codeType = rpcMetricTagsCodeTypeTimeout
		case body.ExecCount > 0:
			codeType = rpcMetricTagsCodeTypeException
		}
		rpcHistogramMetricName := fmt.Sprintf("rpc_%s_handled_seconds", role)
		rpcHistogramPms := toHistogram(
			rpcHistogramMetricName,
			ip,
			data.Timestamp,
			toSecondBuckets(body.IntervalCount),
			statToRPCMetricDims(dims, map[string]string{rpcMetricTagsCodeType: codeType}),
		)
		pms = append(pms, rpcHistogramPms...)

		// 协议数据仅够生成 _bucket / _count 指标，这里需要使用 TotalRspTime 补充 _sum，以构造完整的 Histogram
		pms = append(pms, &promMapper{
			Metrics:    common.MapStr{rpcHistogramMetricName + "_sum": float64(body.TotalRspTime) / 1000},
			Target:     ip,
			Timestamp:  data.Timestamp,
			Dimensions: statToRPCMetricDims(dims, map[string]string{rpcMetricTagsCodeType: codeType}),
		})

		for _, pm := range pms {
			events = append(events, c.ToEvent(token, dataID, pm.AsMapStr()))
		}
	}
	return events
}

// handleStat 处理业务特性指标
func (c tarsConverter) handleProp(token define.Token, dataID int32, ip string, data *define.TarsData) []define.Event {
	pms := make([]*promMapper, 0)
	props := data.Data.(*define.TarsPropertyData).Props
	for head, body := range props {
		moduleName, _ := tokenparser.FromString(head.ModuleName)
		originDims := map[string]string{
			tarsPropertyTagsIp:           head.Ip,
			tarsPropertyTagsModuleName:   moduleName,
			tarsPropertyTagsPropertyName: head.PropertyName,
			tarsPropertyTagsSetName:      head.SetName,
			tarsPropertyTagsSetArea:      head.SetArea,
			tarsPropertyTagsSetId:        head.SetID,
			tarsPropertyTagsSContainer:   head.SContainer,
			tarsPropertyTagsIPropertyVer: strconv.Itoa(int(head.IPropertyVer)),
		}
		// 如果 `ip` 为空，取接收端 `target`。
		if head.Ip == "" {
			originDims["ip"] = ip
		}

		for _, info := range body.VInfo {
			dims := utils.CloneMap(originDims)
			// 补充统计类型作为维度
			dims[tarsPropertyTagsPropertyPolicy] = info.Policy
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

				// Handle Tars Property
				pms = append(pms, toHistogram(metricName, ip, data.Timestamp, toIntBuckets(bucketMap), dims)...)

				// Handle Custom Metrics
				customMetricHistogramPms := toHistogram(
					fmt.Sprintf("%s_%s", head.PropertyName, strings.ToLower(info.Policy)),
					ip,
					data.Timestamp,
					toIntBuckets(bucketMap),
					propToCustomMetricDims(dims, map[string]string{}),
				)
				pms = append(pms, customMetricHistogramPms...)
			default: // Policy -> Max / Min / Avg / Sum / Count
				val, err := strconv.ParseFloat(info.Value, 64)
				if err != nil {
					DefaultMetricMonitor.IncConverterFailedCounter(define.RecordTars, dataID)
					continue
				}

				// Handle Tars Property
				pms = append(pms, &promMapper{
					Metrics:    common.MapStr{metricName: val},
					Target:     ip,
					Timestamp:  data.Timestamp,
					Dimensions: dims,
				})

				// Handle Custom Metrics
				pms = append(pms, &promMapper{
					Metrics:    common.MapStr{fmt.Sprintf("%s_%s", head.PropertyName, strings.ToLower(info.Policy)): val},
					Target:     ip,
					Timestamp:  data.Timestamp,
					Dimensions: propToCustomMetricDims(dims, map[string]string{}),
				})
			}
		}
	}

	events := make([]define.Event, 0, len(pms))
	for _, pm := range pms {
		events = append(events, c.ToEvent(token, dataID, pm.AsMapStr()))
	}
	return events
}
