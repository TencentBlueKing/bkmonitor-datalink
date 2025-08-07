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
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TarsCloud/TarsGo/tars/protocol/res/propertyf"
	"github.com/TarsCloud/TarsGo/tars/protocol/res/statf"
	"github.com/elastic/beats/libbeat/common"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/labels"
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
	resourceTagsVersion       = "version"
)

const (
	rpcMetricNamePrefix      = "origin_rpc"
	rpcMetricAggregatePrefix = "rpc"
)

const (
	rpcMetricTagsCallerServer  = "caller_server"
	rpcMetricTagsCallerService = "caller_service"
	rpcMetricTagsCallerIp      = "caller_ip"
	rpcMetricTagsCalleeServer  = "callee_server"
	rpcMetricTagsCalleeService = "callee_service"
	rpcMetricTagsCalleeMethod  = "callee_method"
	rpcMetricTagsCalleeIp      = "callee_ip"
	rpcMetricTagsCode          = "code"
	rpcMetricTagsCodeType      = "code_type"
)

const (
	rpcMetricTagsCodeTypeSuccess   = "success"
	rpcMetricTagsCodeTypeException = "exception"
	rpcMetricTagsCodeTypeTimeout   = "timeout"
)

const (
	tarsStatTagsRoleClient = "client"
	tarsStatTagsRoleServer = "server"
)

const (
	tarsPropertyTagsPropertyName   = "property_name"
	tarsPropertyTagsPropertyPolicy = "property_policy"
	tarsPropertyTagsIPropertyVer   = "i_property_ver"
)

type bucket struct {
	Val string
	Cnt int32
}

// propNameToNormalizeMetricName 将属性转为标准指标名
func propNameToNormalizeMetricName(propertyName, policy string) string {
	name := strings.Join([]string{propertyName, strings.ToLower(policy)}, "_")
	// 在 NormalizeName 基础上，去掉 :
	return utils.NormalizeName(strings.Replace(name, ":", "", -1))
}

// splitAtLastOnce 根据指定 sep 从右往左切割 s 一次
func splitAtLastOnce(s, sep string) (string, string) {
	lastIndex := strings.LastIndex(s, sep)
	switch lastIndex {
	case -1:
		return s, ""
	case len(s):
		return s[:lastIndex], ""
	default:
		return s[:lastIndex], s[lastIndex+1:]
	}
}

// toBuckets 将分布统计数据转为符合 Prometheus Histogram 格式的分桶数据
func toBuckets(bucketMap map[int32]int32, itoFunc func(int) string) []bucket {
	bucketValList := make([]int, 0, len(bucketMap))
	for val := range bucketMap {
		bucketValList = append(bucketValList, int(val))
	}
	sort.Ints(bucketValList)

	var count int32
	buckets := make([]bucket, 0, len(bucketMap)+1)
	for _, val := range bucketValList {
		cnt, _ := bucketMap[int32(val)]
		count += cnt
		buckets = append(buckets, bucket{itoFunc(val), count})
	}
	inf := strconv.FormatFloat(math.Inf(+1), 'f', -1, 64)
	buckets = append(buckets, bucket{inf, count})
	return buckets
}

// toIntBuckets 将分布统计数据转为符合 Prometheus Histogram 格式，且单位为 Number 的分桶数据
func toIntBuckets(bucketMap map[int32]int32) []bucket {
	return toBuckets(bucketMap, strconv.Itoa)
}

// toSecondBuckets 将分布统计数据转为符合 Prometheus Histogram 格式，且单位为 Seconds 的分桶数据
func toSecondBuckets(bucketMap map[int32]int32) []bucket {
	return toBuckets(bucketMap, func(val int) string { return cast.ToString(float64(val) / 1000) })
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
		bucketMap[cast.ToInt32(p[0])] = cast.ToInt32(p[1])
	}
	return bucketMap
}

// toHistogram 根据分布情况，生成统计指标
func toHistogram(name, target string, timestamp int64, buckets []bucket, dims map[string]string) []*promMapper {
	pms := make([]*promMapper, 0, len(buckets)+1)
	rpcHistogramBucketMetricName := strings.Join([]string{name, "bucket"}, "_")
	for _, b := range buckets {
		dims := utils.CloneMap(dims)
		dims["le"] = b.Val
		pm := &promMapper{
			Metrics:    common.MapStr{rpcHistogramBucketMetricName: b.Cnt},
			Target:     target,
			Timestamp:  timestamp,
			Dimensions: dims,
		}
		pms = append(pms, pm)
	}
	pms = append(pms, &promMapper{
		Metrics:    common.MapStr{strings.Join([]string{name, "count"}, "_"): buckets[len(buckets)-1].Cnt},
		Target:     target,
		Timestamp:  timestamp,
		Dimensions: utils.CloneMap(dims),
	})
	return pms
}

// statHeadToRPCMetricDims 将 Tars Stat 维度转为通用 RPC 模调维度
func statHeadToRPCMetricDims(head *statf.StatMicMsgHead, role string, ip string) map[string]string {
	// 去掉可能存在的 Token，并提取可能存在的 Version 字段。
	calleeServer, _ := tokenparser.FromString(head.SlaveName)
	callerServer, _ := tokenparser.FromString(head.MasterName)
	callerServer, version := splitAtLastOnce(callerServer, "@")

	var instance, serviceName string
	callerIp, calleeIp := head.MasterIp, head.SlaveIp
	if role == tarsStatTagsRoleClient {
		// 主调场景上报指标缺少主调 IP 维度，使用上报 IP 填充
		if callerIp == "" {
			callerIp = ip
		}
		instance = callerIp
		serviceName = callerServer
	} else {
		// 被调场景上报指标缺少被调 IP 维度，使用上报 IP 填充
		if calleeIp == "" {
			calleeIp = ip
		}
		instance = calleeIp
		serviceName = calleeServer
	}

	return map[string]string{
		resourceTagsRPCSystem:   define.RequestTars.S(),
		resourceTagsScopeName:   fmt.Sprintf("%s_metrics", role),
		resourceTagsVersion:     version,
		resourceTagsInstance:    instance,
		resourceTagsServiceName: serviceName,
		// 主调
		rpcMetricTagsCallerServer:  callerServer,
		rpcMetricTagsCallerService: callerServer,
		rpcMetricTagsCallerIp:      callerIp,
		// 被调
		rpcMetricTagsCalleeServer:  calleeServer,
		rpcMetricTagsCalleeService: calleeServer,
		rpcMetricTagsCalleeIp:      calleeIp,
		rpcMetricTagsCalleeMethod:  head.InterfaceName,
		// 返回码
		rpcMetricTagsCode: strconv.Itoa(int(head.ReturnValue)),
	}
}

// propHeadToCustomMetricDims 将 Tars Property 维度转为自定义指标维度
func propHeadToCustomMetricDims(head *propertyf.StatPropMsgHead, ip string) map[string]string {
	instance := head.Ip
	if instance == "" {
		// 原始数据中可能没有 IP 维度，使用上报 IP 填充。
		instance = ip
	}

	serviceName, _ := tokenparser.FromString(head.ModuleName)
	return map[string]string{
		resourceTagsRPCSystem:        define.RequestTars.S(),
		resourceTagsScopeName:        fmt.Sprintf("%s_property", define.RequestTars.S()),
		resourceTagsInstance:         instance,
		resourceTagsServiceName:      serviceName,
		resourceTagsContainerName:    head.SContainer,
		tarsPropertyTagsIPropertyVer: strconv.Itoa(int(head.IPropertyVer)),
		tarsPropertyTagsPropertyName: head.PropertyName,
	}
}

// TarsEvent is a struct that embeds CommonEvent.
type TarsEvent struct {
	define.CommonEvent
}

// RecordType returns the type of record.
func (e TarsEvent) RecordType() define.RecordType {
	return define.RecordTars
}

type statPK struct {
	dataID int32
	hash   uint64
}

type stat struct {
	token        define.Token
	role         string
	target       string
	timestamp    int64
	execCount    int32
	timeoutCount int32
	successCount int32
	totalRspTime int64
	bucketMap    map[int32]int32
	dimensions   map[string]string
}

// newStat 创建一个新的 stat 实例
func newStat(token define.Token, role, target string, timestamp int64, dims map[string]string, body statf.StatMicMsgBody) *stat {
	return &stat{
		token:        token,
		role:         role,
		target:       target,
		timestamp:    timestamp,
		execCount:    body.ExecCount,
		timeoutCount: body.TimeoutCount,
		successCount: body.Count,
		totalRspTime: body.TotalRspTime,
		bucketMap:    body.IntervalCount,
		dimensions:   dims,
	}
}

// GetDataID 返回数据 ID
func (s *stat) GetDataID() int32 {
	return s.token.MetricsDataId
}

// PK 返回 stat 的哈希值
func (s *stat) PK() statPK {
	return statPK{s.GetDataID(), labels.HashFromMap(s.dimensions)}
}

// Copy 创建一个新的 stat 实例，并复制当前实例的内容
func (s *stat) Copy() *stat {
	newStat := &stat{
		token:        s.token,
		role:         s.role,
		target:       s.target,
		timestamp:    s.timestamp,
		execCount:    s.execCount,
		timeoutCount: s.timeoutCount,
		successCount: s.successCount,
		totalRspTime: s.totalRspTime,
		dimensions:   utils.CloneMap(s.dimensions),
		bucketMap:    make(map[int32]int32, len(s.bucketMap)),
	}
	for k, v := range s.bucketMap {
		newStat.bucketMap[k] = v
	}
	return newStat
}

// DropTags 删除指定维度
func (s *stat) DropTags(tags []string) *stat {
	for _, tag := range tags {
		if _, ok := s.dimensions[tag]; ok {
			delete(s.dimensions, tag)
		}
	}
	return s
}

// UpdateFrom 从另一个 stat 实例更新当前实例的统计数据
func (s *stat) UpdateFrom(other *stat) {
	s.execCount += other.execCount
	s.timeoutCount += other.timeoutCount
	s.successCount += other.successCount
	s.totalRspTime += other.totalRspTime
	for k, v := range other.bucketMap {
		s.bucketMap[k] += v
	}
}

// ToEvents 将 stat 转换为 Event 列表
func (s *stat) ToEvents(metricNamePrefix string) []define.Event {
	var pms []*promMapper

	codeTypeReqCntMap := map[string]int32{
		rpcMetricTagsCodeTypeException: s.execCount,
		rpcMetricTagsCodeTypeTimeout:   s.timeoutCount,
		rpcMetricTagsCodeTypeSuccess:   s.successCount,
	}
	for codeType, reqCnt := range codeTypeReqCntMap {
		if reqCnt == 0 {
			continue
		}

		pms = append(pms, &promMapper{
			Metrics: common.MapStr{
				strings.Join([]string{metricNamePrefix, s.role, "handled_total"}, "_"): reqCnt,
			},
			Target:     s.target,
			Timestamp:  s.timestamp,
			Dimensions: utils.MergeMaps(s.dimensions, map[string]string{rpcMetricTagsCodeType: codeType}),
		})
	}

	// ReturnValue = 0 也可能是超时 or 异常，而协议的分桶数据不区分返回码状态，所以此处只能大致判断，写一个预估的返回码类型。
	codeType := rpcMetricTagsCodeTypeSuccess
	switch {
	case s.execCount > 0:
		codeType = rpcMetricTagsCodeTypeException
	case s.timeoutCount > 0:
		codeType = rpcMetricTagsCodeTypeTimeout
	}

	rpcHistogramMetricName := strings.Join([]string{metricNamePrefix, s.role, "handled_seconds"}, "_")
	dims := utils.MergeMaps(s.dimensions, map[string]string{rpcMetricTagsCodeType: codeType})
	rpcHistogramPms := toHistogram(rpcHistogramMetricName, s.target, s.timestamp, toSecondBuckets(s.bucketMap), dims)
	pms = append(pms, rpcHistogramPms...)

	// 协议数据仅够生成 _bucket / _count 指标，这里需要使用 TotalRspTime 补充 _sum，以构造完整的 Histogram
	rpcHistogramSumMetricName := strings.Join([]string{rpcHistogramMetricName, "sum"}, "_")
	pms = append(pms, &promMapper{
		Metrics:    common.MapStr{rpcHistogramSumMetricName: cast.ToFloat64(s.totalRspTime) / 1000},
		Target:     s.target,
		Timestamp:  s.timestamp,
		Dimensions: dims,
	})

	var events []define.Event
	for _, pm := range pms {
		events = append(events, TarsEvent{define.NewCommonEvent(s.token, s.GetDataID(), pm.AsMapStr())})
	}
	return events
}

type aggregator struct {
	wg                    sync.WaitGroup
	ctx                   context.Context
	cancel                context.CancelFunc
	ch                    chan *stat
	buffer                map[statPK]*stat
	interval              time.Duration
	setGatherFuncOnce     sync.Once
	gatherFunc            define.GatherFunc
	scopeNameToIgnoreTags map[string][]string
}

// newAggregator 创建聚合器实例
func newAggregator(interval time.Duration, scopeNameToIgnoreTags map[string][]string) *aggregator {
	ctx, cancel := context.WithCancel(context.Background())
	a := &aggregator{
		ctx:                   ctx,
		cancel:                cancel,
		ch:                    make(chan *stat, 256),
		buffer:                make(map[statPK]*stat),
		interval:              interval,
		scopeNameToIgnoreTags: scopeNameToIgnoreTags,
	}

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.Run()
	}()
	return a
}

// SetGatherFunc 设置聚合器的 GatherFunc
func (a *aggregator) SetGatherFunc(f define.GatherFunc) {
	a.setGatherFuncOnce.Do(func() { a.gatherFunc = f })
}

// Run 启动聚合器
func (a *aggregator) Run() {
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	for {
		select {
		case s := <-a.ch:
			a.aggregate(s)
		case <-ticker.C:
			a.exportAndClean()
		case <-a.ctx.Done():
			return
		}
	}
}

func (a *aggregator) Stop() {
	a.cancel()
	a.wg.Wait()
}

// Aggregate 接收 stat 并将其发送到聚合器通道
func (a *aggregator) Aggregate(s *stat) {
	a.ch <- s
}

// aggregate 样本点聚合
func (a *aggregator) aggregate(s *stat) {
	stat := s.Copy()
	pk := stat.DropTags(a.scopeNameToIgnoreTags[s.dimensions[resourceTagsScopeName]]).PK()
	if _, ok := a.buffer[pk]; !ok {
		a.buffer[pk] = stat
	} else {
		a.buffer[pk].UpdateFrom(stat)
	}
}

// ExportAndClean 导出并清理缓冲区中的数据
func (a *aggregator) exportAndClean() {
	var events []define.Event
	for pk, s := range a.buffer {
		events = append(events, s.ToEvents(rpcMetricAggregatePrefix)...)
		delete(a.buffer, pk)
	}

	if a.gatherFunc != nil && len(events) > 0 {
		a.gatherFunc(events...)
	}
}

type tarsConverter struct {
	isDropOriginal bool
	conf           TarsConfig
	aggregator     *aggregator
}

func NewTarsConverter(config TarsConfig) EventConverter {
	scopeNameToIgnoreTags := make(map[string][]string)
	for _, tagIgnore := range config.TagIgnores {
		scopeNameToIgnoreTags[tagIgnore.ScopeName] = append(scopeNameToIgnoreTags[tagIgnore.ScopeName], tagIgnore.Tags...)
	}

	if config.DisableAggregate {
		return tarsConverter{conf: config, aggregator: nil}
	}

	return tarsConverter{
		conf:       config,
		aggregator: newAggregator(config.AggregateInterval, scopeNameToIgnoreTags),
	}
}

func (c tarsConverter) Clean() {
	if c.aggregator != nil {
		c.aggregator.Stop()
	}
}

func (c tarsConverter) ToEvent(token define.Token, dataId int32, data common.MapStr) define.Event {
	return TarsEvent{define.NewCommonEvent(token, dataId, data)}
}

func (c tarsConverter) ToDataID(record *define.Record) int32 {
	return record.Token.MetricsDataId
}

func (c tarsConverter) Convert(record *define.Record, f define.GatherFunc) {
	if c.aggregator != nil {
		c.aggregator.SetGatherFunc(f)
	}

	var events []define.Event
	dataID := c.ToDataID(record)
	data := record.Data.(*define.TarsData)
	if data.Type == define.TarsPropertyType {
		events = c.handleProp(record.Token, dataID, record.RequestClient.IP, data)
	} else {
		events = c.handleStat(record.Token, record.RequestClient.IP, data)
	}
	if len(events) > 0 {
		f(events...)
	}
}

func (c tarsConverter) statToEvents(stat *stat) []define.Event {
	if c.aggregator != nil {
		c.aggregator.Aggregate(stat)
	}

	if c.conf.IsDropOriginal {
		return nil
	}

	return stat.ToEvents(rpcMetricNamePrefix)
}

// handleStat 处理服务统计指标
func (c tarsConverter) handleStat(token define.Token, ip string, data *define.TarsData) []define.Event {
	var events []define.Event
	sd := data.Data.(*define.TarsStatData)

	role := tarsStatTagsRoleServer
	if sd.FromClient {
		role = tarsStatTagsRoleClient
	}

	for head, body := range sd.Stats {
		dims := statHeadToRPCMetricDims(&head, role, ip)
		stat := newStat(token, role, ip, data.Timestamp, dims, body)
		events = append(events, c.statToEvents(stat)...)

		if role == tarsStatTagsRoleClient {
			calleeServer, ok := dims[rpcMetricTagsCalleeServer]
			if !ok || calleeServer == "" || calleeServer == "." {
				continue
			}

			calleeIp, ok := dims[rpcMetricTagsCalleeIp]
			if !ok || calleeIp == "" {
				continue
			}

			serverDims := utils.MergeMaps(dims, map[string]string{
				resourceTagsServiceName: calleeServer,
				resourceTagsInstance:    calleeIp,
				resourceTagsScopeName:   "server_metrics",
			})
			// 从 Client 切换成 Server 视角，target 取值从 ip 调整为 calleeIp，避免 target x calleeIp 不一致导致高基数。
			serverStatFromClient := newStat(token, tarsStatTagsRoleServer, calleeIp, data.Timestamp, serverDims, body)
			events = append(events, c.statToEvents(serverStatFromClient)...)
		}
	}
	return events
}

// handleStat 处理业务特性指标
func (c tarsConverter) handleProp(token define.Token, dataID int32, ip string, data *define.TarsData) []define.Event {
	pms := make([]*promMapper, 0)
	for head, body := range data.Data.(*define.TarsPropertyData).Props {
		commonDims := propHeadToCustomMetricDims(&head, ip)
		for _, info := range body.VInfo {
			dims := utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: info.Policy})
			switch info.Policy {
			case "Distr":
				bucketMap := toBucketMap(info.Value)
				if len(bucketMap) == 0 {
					logger.Warnf(
						"[handleProp] empty distrMap, dataID=%d, ip=%v, propertyName=%s, Distr=%s",
						dataID, ip, head.PropertyName, info.Value)
					continue
				}

				customMetricHistogramPms := toHistogram(
					propNameToNormalizeMetricName(head.PropertyName, info.Policy),
					ip,
					data.Timestamp,
					toIntBuckets(bucketMap),
					dims,
				)
				pms = append(pms, customMetricHistogramPms...)
			default: // Policy -> Max / Min / Avg / Sum / Count
				pms = append(pms, &promMapper{
					Metrics:    common.MapStr{propNameToNormalizeMetricName(head.PropertyName, info.Policy): cast.ToFloat64(info.Value)},
					Target:     ip,
					Timestamp:  data.Timestamp,
					Dimensions: dims,
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
