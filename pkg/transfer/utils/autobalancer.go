// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/cespare/xxhash"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

var (
	autoBalancerEffectedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "autobalancer_effected_total",
		Help:      "Auto balancer effected total",
	})
	autoBalancerBestOverflow = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: define.AppName,
		Name:      "autobalancer_best_overflow",
		Help:      "Auto balancer best overflow",
	})
	autoBalancerBestTopPercent = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: define.AppName,
		Name:      "autobalancer_best_top_percent",
		Help:      "Auto balancer best top percent",
	})
	autoBalancerMaxFlowRatio = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: define.AppName,
		Name:      "autobalancer_max_flow_ratio",
		Help:      "Auto balancer max flow ratio",
	})
	autoBalancerTargets = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: define.AppName,
		Name:      "autobalancer_targets",
		Help:      "Auto balancer targets count",
	})
	autoBalancerDifferentTargets = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: define.AppName,
		Name:      "autobalancer_different_targets",
		Help:      "Auto balancer different targets count",
	})
	autoBalancerZeroFlowTargets = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: define.AppName,
		Name:      "autobalancer_zero_flow_targets",
		Help:      "Auto balancer zero flow targets count",
	})
	autoBalancerFluctuation = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: define.AppName,
		Name:      "autobalancer_fluctuation",
		Help:      "Auto balancer fluctuation",
	})
)

func init() {
	prometheus.MustRegister(
		autoBalancerEffectedTotal,
		autoBalancerBestOverflow,
		autoBalancerBestTopPercent,
		autoBalancerMaxFlowRatio,
		autoBalancerTargets,
		autoBalancerZeroFlowTargets,
		autoBalancerFluctuation,
		autoBalancerDifferentTargets,
	)
}

type autoBalancer struct {
	fluctuation float64                          // fluctuation 允许在判断两次流量变化的最大百分比
	getFlow     func() (define.FlowItems, error) // getFlow 流量获取函数
	getConf     func() define.BalanceConfig      // getConf 配置获取函数
	logPath     string
	logger      *logging.Logger
	first       bool
	executed    int
	forceRound  int
}

func NewAutoBalancer(fluctuation float64, forceRound int, getFlow func() (define.FlowItems, error), getConf func() define.BalanceConfig, logPath string) define.Balancer {
	balancer := &autoBalancer{
		fluctuation: fluctuation,
		getFlow:     getFlow,
		getConf:     getConf,
		logPath:     logPath,
		forceRound:  forceRound,
		first:       true,
	}

	go func() {
		for range time.Tick(30 * time.Second) {
			balancer.updateConf()
		}
	}()
	return balancer
}

func (ab *autoBalancer) updateConf() {
	if ab.getConf == nil {
		return
	}

	conf := ab.getConf()
	ab.fluctuation = conf.Fluctuation
	ab.forceRound = conf.ForceRound
}

func (ab *autoBalancer) forceExecuted() bool {
	// 如果收到强制调度信号 也需要执行
	if define.TryBalanceNotify() {
		return true
	}
	// 第 3 个周期会强制执行一次调度 此时流量应该都已经被记录了 避免第一个执行周期等待太久
	if ab.executed == 3 {
		return true
	}

	// 周期性强制调度
	if ab.forceRound > 0 && ab.executed%ab.forceRound == 0 {
		return true
	}
	return false
}

// Balance 重新生成分配方案
func (ab *autoBalancer) Balance(plan define.PlanWithFlows, items, nodes []define.IDer) (define.IDerMapDetailed, define.FlowItems, define.AutoError) {
	defer func() { ab.first = false }()

	var flows define.FlowItems
	var err error

	iderMap := define.NewIDerMapDetailed()
	if len(nodes) <= 0 {
		return iderMap, flows, define.AutoErrorNoNodes
	}

	sort.Slice(items, func(i, j int) bool { return items[i].ID() < items[j].ID() })
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID() < nodes[j].ID() })

	prevNodes := make([]define.IDer, 0)
	for k := range plan.IDers.All {
		prevNodes = append(prevNodes, k)
	}

	flows, err = ab.getFlow()
	if err != nil {
		logging.Errorf("failed to get dataid flow, err:%v", err)
		return iderMap, flows, define.AutoErrorConsulFailed
	}

	// 记录 balance 执行次数 上层 flowticker 默认每两分钟会执行一次 balance
	ab.executed++

	prevItems := make([]define.IDer, 0)
	for _, v := range plan.IDers.All {
		prevItems = append(prevItems, v...)
	}

	ab.WriteScheduleDetailed("dispatch-items", ab.DispatchItemLog(len(prevNodes), len(prevItems), len(nodes), len(items)))

	// 第一次执行 auto balance 的时候会强制更新 不检测波动
	if !ab.first {
		// 当节点和 dataid 都没有发生改变的时候 需要额外判断流量是否有变化
		if ab.isSameIderList(prevNodes, nodes) && ab.isSameIderList(prevItems, items) {
			serviceFlows := flows.SumPercentBy(define.FlowItemKeyService)
			fluctuated := ab.isFlowFluctuate(plan.Flows, serviceFlows) || ab.forceExecuted()
			// 如果流量没有抖动 那直接跳过
			if !fluctuated {
				return plan.IDers, flows, define.AutoErrorNil
			}
		}
	}

	// 流量抖动 或者 数量发生变化
	autoBalancerEffectedTotal.Inc()

	// 对于不存在的 key 返回空 slice 而不是 nil
	getPrevDataIds := func(k define.IDer) []define.IDer {
		// 比较距离的时候要用不带流量为 0 的 dataid 的分组情况 因为 0 是在距离分组计算之后再进行 hash 的
		// 否则在编辑距离计算的话会放大这个距离 可能会导致误差
		if v, ok := plan.IDers.WithFlow[k.ID()]; ok {
			return v
		}
		return []define.IDer{}
	}

	nodeLen := len(nodes)
	draft := ab.getBestDraft(flows, items, nodeLen) // 新分配草图
	prevNodes = nodes                               // 矩阵对齐

	bestSolution := make(map[define.IDer][]define.IDer)
	var bestPercent float64
	differenceTotal := math.MaxInt

	topPercents := []float64{.1, .2, .3, .4, .5, .6, .7, .8, .9, 1}
	for _, percent := range topPercents {
		selected := make(map[int]struct{})
		solution := make(map[define.IDer][]define.IDer)
		for i := 0; i < nodeLen; i++ {
			group := draft.GetGroup(i)
			minDifference := math.MaxInt
			nodeIdx := -1
			for j := 0; j < nodeLen; j++ {
				// 已经选中的 node 跳过
				if _, ok := selected[j]; ok {
					continue
				}
				difference := ab.calcTopDifference(getPrevDataIds(prevNodes[j]), group, percent)
				if difference < minDifference {
					minDifference = difference
					nodeIdx = j
				}
			}
			// 标记选中
			selected[nodeIdx] = struct{}{}
			solution[prevNodes[nodeIdx]] = group
		}

		var differences int
		for i := 0; i < nodeLen; i++ {
			differences += ab.calcAllDifference(getPrevDataIds(prevNodes[i]), solution[prevNodes[i]])
		}
		// 求差异最小的方案 即调度开销最小的方案
		if differences < differenceTotal {
			differenceTotal = differences
			bestSolution = solution
			bestPercent = percent
		}
	}

	// 记录带流量的 dataid 分配结果
	copySolution := make(map[int][]define.IDer)
	for k, v := range bestSolution {
		copySolution[k.ID()] = v
	}

	iderMap.WithFlow = copySolution
	draft.Difference = differenceTotal
	autoBalancerDifferentTargets.Set(float64(differenceTotal))
	autoBalancerBestTopPercent.Set(bestPercent)
	logging.Infof("autobalancer targets: total=%v, difference=%v, bestPercent=%v", len(items), differenceTotal, bestPercent)

	iderMap.All = ab.handleZeroFlows(draft, nodes, bestSolution)
	ab.WriteScheduleDetailed("draft-log", draft.Log())
	ab.WriteScheduleDetailed("draft-withflow", WithFlow(iderMap.WithFlow).Log())
	ab.WriteScheduleDetailed("draft-solution", Solution(iderMap.All).Log())

	return iderMap, flows, define.AutoErrorNil
}

func (ab *autoBalancer) calcAllDifference(prevIders, currIders []define.IDer) int {
	return ab.calcTopDifference(prevIders, currIders, 1)
}

// calcTopDifference 计算数组之间的差距
func (ab *autoBalancer) calcTopDifference(prevIders, currIders []define.IDer, topPercent float64) int {
	// topPercent 用于取 topK ider 进行比较 确保头部的 dataid 不会漂移
	l := int(float64(len(currIders)) * topPercent)
	if l >= len(currIders) {
		l = len(currIders) - 1
	}

	// 没必要比较
	if l <= 0 {
		return 0
	}

	set := make(map[int]struct{})
	for _, id := range prevIders {
		set[id.ID()] = struct{}{}
	}
	var total int
	for index, ider := range currIders {
		if index > l {
			break
		}
		if _, ok := set[ider.ID()]; !ok {
			total++
		}
	}
	return total
}

func (ab *autoBalancer) handleZeroFlows(draft Draft, nodes []define.IDer, solution map[define.IDer][]define.IDer) map[define.IDer][]define.IDer {
	var zeroFlows float64
	nodeLen := len(nodes)
	for dataid, cnt := range draft.zeroFlows {
		// 计算一个 dataid 的基准哈希
		hashcode := xxhash.Sum64String(fmt.Sprintf("dataid:%d", dataid))
		for i := 0; i < cnt; i++ {
			// 记录 dataid hash 值 尽量均衡分配 每一个分区都是上一个分区的偏移+1
			hashcode += 1
			idx := hashcode % uint64(nodeLen)
			ider, ok := draft.GetOriginIDer(dataid)
			if !ok {
				continue
			}
			zeroFlows += 1
			solution[nodes[idx]] = append(solution[nodes[idx]], ider)
		}
	}
	autoBalancerZeroFlowTargets.Set(zeroFlows)
	return solution
}

func (ab *autoBalancer) DispatchItemLog(prevNodes, prevItems, currNodes, currItems int) string {
	info := struct {
		PrevNodes int `json:"prev_nodes"`
		PrevItems int `json:"prev_items"`
		CurrNodes int `json:"curr_nodes"`
		CurrItems int `json:"curr_items"`
	}{
		PrevNodes: prevNodes,
		PrevItems: prevItems,
		CurrNodes: currNodes,
		CurrItems: currItems,
	}
	bs, _ := json.Marshal(info)
	return string(bs)
}

func (ab *autoBalancer) initLogger() {
	conf := define.NewViperConfiguration(viper.New())
	conf.SetDefault("logger.level", "info")
	conf.SetDefault("logger.out.name", "file")
	conf.SetDefault("logger.out.options.file", ab.logPath)
	conf.SetDefault("logger.out.options.maxdays", 5)
	conf.SetDefault("logger.out.options.maxsize", 268435456)
	conf.SetDefault("logger.out.options.level", "info")

	logger := logging.NewLogger(conf)
	ab.logger = logger
}

func (ab *autoBalancer) WriteScheduleDetailed(name, arg string) {
	if ab.logger == nil {
		if ab.logPath == "" {
			return
		}
		ab.initLogger()
	}

	if ab.logger != nil {
		ab.logger.Infof("%s: %s", name, arg)
	}
}

// isFlowFluctuate 判断流量波动情况 如果流量波动不大的话并不需要做调整
func (ab *autoBalancer) isFlowFluctuate(prev map[string]float64, curr map[string]float64) bool {
	if len(prev) == 0 || len(curr) == 0 {
		return false
	}

	minv, maxv := math.MaxFloat64, float64(0)
	for k, v := range prev {
		delta := curr[k] - v
		if delta <= minv {
			minv = delta
		}
		if delta > maxv {
			maxv = delta
		}
	}

	r := math.Abs(maxv) + math.Abs(minv)
	autoBalancerFluctuation.Set(r)
	greater := r > ab.fluctuation
	logging.Infof("autobalancer fluctuation: %v, fluctuated: %v", r, greater)

	info := struct {
		Fluctuation float64            `json:"fluctuation"`
		Rebalanced  bool               `json:"rebalanced"`
		PrevFlow    map[string]float64 `json:"prev_flow"`
		NextFlow    map[string]float64 `json:"next_flow"`
	}{
		Fluctuation: r,
		Rebalanced:  greater,
		PrevFlow:    prev,
		NextFlow:    curr,
	}
	bs, _ := json.Marshal(info)
	ab.WriteScheduleDetailed("fluctuation", string(bs))

	return greater
}

// isSameIderList 判断节点列表是否完全相同
func (ab *autoBalancer) isSameIderList(a, b []define.IDer) bool {
	if len(a) != len(b) {
		return false
	}

	ma := make(map[int]int)
	for _, ai := range a {
		ma[ai.ID()]++
	}

	mb := make(map[int]int)
	for _, bi := range b {
		mb[bi.ID()]++
	}

	for k := range ma {
		if _, ok := mb[k]; !ok {
			return false
		}

		if ma[k] != mb[k] {
			return false
		}
	}

	return true
}

type IdWithFlow struct {
	DataID int `json:"dataid"`
	Flow   int `json:"flow"`
}

func (f IdWithFlow) ID() int {
	return f.DataID
}

type IderWithFlow struct {
	Ider define.IDer
	Flow int
}

type FlowGroup struct {
	Group   []IdWithFlow `json:"group"`
	Weight  int          `json:"weight"`
	Percent float64      `json:"percent"`
}

func (fg FlowGroup) IDs() []int {
	ret := make([]int, 0)
	for _, g := range fg.Group {
		ret = append(ret, g.DataID)
	}
	return ret
}

func (fg FlowGroup) Flows() int {
	var total int
	for _, g := range fg.Group {
		total += g.Flow
	}
	return total
}

type Solution map[define.IDer][]define.IDer

func (s Solution) Get(k define.IDer) []int {
	ret := make([]int, 0)
	for _, v := range s[k] {
		ret = append(ret, v.ID())
	}

	sort.Ints(ret)
	return ret
}

func (s Solution) Log() string {
	type D struct {
		Service string `json:"service"`
		DataIDs []int  `json:"datadids"`
	}

	ds := make([]D, 0)
	for k := range s {
		ds = append(ds, D{Service: fmt.Sprintf("bkmonitorv3-%d", k.ID()), DataIDs: s.Get(k)})
	}

	bs, _ := json.Marshal(ds)
	return string(bs)
}

type WithFlow map[int][]define.IDer

func (s WithFlow) Get(k int) []int {
	ret := make([]int, 0)
	for _, v := range s[k] {
		ret = append(ret, v.ID())
	}

	sort.Ints(ret)
	return ret
}

func (s WithFlow) Log() string {
	type D struct {
		Service string `json:"service"`
		DataIDs []int  `json:"datadids"`
		Count   int    `json:"count"`
	}

	ds := make([]D, 0)
	for k := range s {
		dids := s.Get(k)
		ds = append(ds, D{
			Service: fmt.Sprintf("bkmonitorv3-%d", k),
			DataIDs: dids,
			Count:   len(dids),
		})
	}

	bs, _ := json.Marshal(ds)
	return string(bs)
}

type Draft struct {
	TotalWeight int
	AvgWeight   int
	Overflow    float64
	MaxRatio    float64
	Difference  int
	Groups      []FlowGroup
	original    []define.IDer
	zeroFlows   map[int]int
}

func (d Draft) GetGroup(i int) []define.IDer {
	iderFlows := make([]IderWithFlow, 0)
	for _, v := range d.Groups[i].Group {
		// 类型还原 dataid 是 repeat 出来的 所以相同 dataid 之间无差
		for _, org := range d.original {
			if org.ID() == v.DataID {
				iderFlows = append(iderFlows, IderWithFlow{Ider: org, Flow: v.Flow})
				break
			}
		}
	}

	// 按流量倒序排序
	sort.Slice(iderFlows, func(i, j int) bool {
		return iderFlows[i].Flow > iderFlows[j].Flow
	})

	// 类型转换成 define.IDer slice
	iders := make([]define.IDer, 0, len(iderFlows))
	for _, iderFlow := range iderFlows {
		iders = append(iders, iderFlow.Ider)
	}
	return iders
}

func (d Draft) GetOriginIDer(dataid int) (define.IDer, bool) {
	for _, org := range d.original {
		if org.ID() == dataid {
			return org, true
		}
	}

	return nil, false
}

// SortGroups 排序 groups 保证每次输出的顺序要一致
func (d Draft) SortGroups() {
	sort.Slice(d.Groups, func(i, j int) bool {
		if len(d.Groups[i].Group) < len(d.Groups[j].Group) {
			return true
		}
		if len(d.Groups[i].Group) > len(d.Groups[j].Group) {
			return false
		}

		// 长度相等
		n := len(d.Groups[i].Group)
		if n <= 0 {
			return true
		}

		for k := 0; k < n; k++ {
			if d.Groups[i].Group[k].DataID == d.Groups[j].Group[k].DataID {
				continue
			}
			if d.Groups[i].Group[k].DataID < d.Groups[j].Group[k].DataID {
				return true
			}
			if d.Groups[i].Group[k].DataID > d.Groups[j].Group[k].DataID {
				return false
			}
		}
		return true
	})
}

func (d Draft) Log() string {
	info := struct {
		TotalWeight int         `json:"total_weight"`
		AvgWeight   int         `json:"avg_weight"`
		Difference  int         `json:"difference"`
		MaxRatio    float64     `json:"max_ratio"`
		Groups      []FlowGroup `json:"groups"`
	}{
		TotalWeight: d.TotalWeight,
		AvgWeight:   d.AvgWeight,
		Difference:  d.Difference,
		MaxRatio:    d.MaxRatio,
		Groups:      d.Groups,
	}
	bs, _ := json.Marshal(info)
	return string(bs)
}

type groupSet struct {
	set map[int]map[int]struct{}
}

func (g *groupSet) Set(i, dataid int) {
	_, ok := g.set[i]
	if !ok {
		g.set[i] = map[int]struct{}{}
	}
	g.set[i][dataid] = struct{}{}
}

func (g *groupSet) Get(i, dataid int) bool {
	_, ok := g.set[i]
	if !ok {
		return false
	}

	_, ok = g.set[i][dataid]
	return ok
}

func (ab *autoBalancer) sortGroupIndex(groups [][]IdWithFlow) []int {
	type R struct {
		index int
		flow  int
	}
	var rs []R

	for i, item := range groups {
		flowsum := 0
		for _, g := range item {
			flowsum += g.Flow
		}
		rs = append(rs, R{index: i, flow: flowsum})
	}

	sort.Slice(rs, func(i, j int) bool {
		return rs[i].flow < rs[j].flow
	})

	var ks []int
	for _, r := range rs {
		ks = append(ks, r.index)
	}

	return ks
}

// DataIdCountLog 日志信息记录
func (ab *autoBalancer) DataIdCountLog(counts map[int]int, mapFlows map[string]int) string {
	type info struct {
		DataID int `json:"dataid"`
		Count  int `json:"count"`
		Flow   int `json:"flow"`
	}
	var ks []int
	for k := range counts {
		ks = append(ks, k)
	}
	sort.Ints(ks)

	var infos []info
	for _, k := range ks {
		infos = append(infos, info{
			DataID: k,
			Count:  counts[k],
			Flow:   mapFlows[strconv.Itoa(k)],
		})
	}
	bs, _ := json.Marshal(infos)
	return string(bs)
}

func (ab *autoBalancer) getBestDraft(flows define.FlowItems, items []define.IDer, n int) Draft {
	// 在多次测试结果下得到一个结论 即 bestOverflow 与 bestMaxRatio 不可能同时出现
	// 1）如果为了流量均衡 即必定会带来更大的调度开销
	// 2）如果为了更小的调度开销 即必定不会有最佳的均衡效果
	// 从实践来看 2）的重要性要高于 1) 即我们应该在「大体均衡」的前提下 确保最小的调动开销 不然会带来一个较高的流量高峰 给下游组件和 Kafka 造成压力
	const bestOverflow = 0 // 最佳 overflow 阈值
	return ab.getDraft(flows, items, n, bestOverflow)
}

// getDraft 在打散 dataid 的前提下尽量均衡分配
func (ab *autoBalancer) getDraft(flows define.FlowItems, items []define.IDer, n int, overflow float64) Draft {
	mapFlows := flows.AvgBy(define.FlowItemKeyDataID)
	// counts 存储 dataid 的个数 即分区个数
	// key: dataid
	// value: partition count of dataid
	counts := make(map[int]int)

	// 去重后的 dataid 数组
	idWithFlows := make([]IdWithFlow, 0)
	autoBalancerTargets.Set(float64(len(items)))

	var (
		groupSum  int
		avgWeight int
		total     int
		group     []IdWithFlow
		groups    [][]IdWithFlow
		gset      = &groupSet{set: map[int]map[int]struct{}{}}
	)

	for _, item := range items {
		flow := mapFlows[strconv.Itoa(item.ID())]

		if _, ok := counts[item.ID()]; !ok {
			idWithFlows = append(idWithFlows, IdWithFlow{DataID: item.ID(), Flow: flow})
		}
		counts[item.ID()] += 1
		total += flow
	}

	ab.WriteScheduleDetailed("dataid-count", ab.DataIdCountLog(counts, mapFlows))

	sort.Slice(idWithFlows, func(i, j int) bool {
		return idWithFlows[i].Flow < idWithFlows[j].Flow
	})

	// 允许 1+weightOverflow 的向上浮动偏差
	avgWeight = int(float64(total/n) * (1 + overflow))
	gaw := avgWeight // 全局整体平均值 因为平均权重可能会被重新 所以保留原始值

	// 重新计算平均权重
	resetAvgWeight := func() {
		var newTotal int
		for i := 0; i < len(idWithFlows); i++ {
			for j := 0; j < counts[idWithFlows[i].DataID]; j++ {
				newTotal += mapFlows[strconv.Itoa(idWithFlows[i].ID())]
			}
		}

		if n-len(groups) > 0 {
			avgWeight = int(float64(newTotal/(n-len(groups))) * (1 + overflow))
		}
	}

	// 状态重置
	resetState := func() {
		groups = append(groups, group)
		group = []IdWithFlow{}
		groupSum = 0
	}

	// cursor 记录右指针移动的位置
	var cursor int
	for i := 0; i < n; i++ {
		var overMax bool
		// 右指针向左移动
		for j := len(idWithFlows) - 1; j >= 0; j-- {
			cursor = j
			// 如果该 dataid 已经被取完了 那忽略
			if counts[idWithFlows[j].DataID] == 0 {
				continue
			}
			// 无流量的 dataid 不参与本次分配
			if idWithFlows[j].Flow == 0 {
				continue
			}

			// 如果超过分组的平均权重 那分两种情况
			// 如果 group 的长度为 0 则代表这是第一进入 group 的元素 即使它超过了平均阈值也木得办法 梭哈 然后切新分组
			if groupSum+idWithFlows[j].Flow >= avgWeight {
				if len(group) == 0 {
					group = append(group, idWithFlows[j])
					counts[idWithFlows[j].DataID] -= 1
					resetState()
					resetAvgWeight() // 这种情况下需要重新计算平均权重 避免因超大值影响整体分配
					overMax = true
					break
				}
			} else {
				// 如果还没达到平均权重 则继续向右移动左指针
				groupSum += idWithFlows[j].Flow
				group = append(group, idWithFlows[j])
				counts[idWithFlows[j].DataID] -= 1
			}
		}

		// 当已经出现超大权重值的时候 就不需要再移动左指针了 因为该分组已经分配完毕
		if overMax {
			continue
		}

		// 左指针向右移动
		for j := 0; j < len(idWithFlows); j++ {
			// 左右指针相遇 退出循环
			if j == cursor {
				break
			}

			// 如果该 dataid 已经被取完了 那忽略
			if counts[idWithFlows[j].DataID] == 0 {
				continue
			}
			// 无流量的 dataid 不参与本次分配
			if idWithFlows[j].Flow == 0 {
				continue
			}

			// 使分组权重尽量靠近平均权重
			if groupSum+idWithFlows[j].Flow < avgWeight {
				groupSum += idWithFlows[j].Flow
				group = append(group, idWithFlows[j])
				counts[idWithFlows[j].DataID] -= 1
			}
		}

		// 当左指针也没得移动的话 那应该切割新分组
		resetState()
	}

	// 记录 group 元素集合
	for i, gs := range groups {
		for _, g := range gs {
			gset.Set(i, g.DataID)
		}
	}

	// 还没分完 但分组数已经到了 那活最少的人挑权重最大的元素
	// 这里还需要考虑 kafka 的特性 当 partition 的数量大于 transfer 实例数量的时候 数值上分配结果虽然正确
	// 但实际上的话 worker 消费的时候仍然是会不平衡 所以需要新增一个互斥的逻辑
	for i := len(idWithFlows) - 1; i >= 0; i-- {
		if idWithFlows[i].Flow == 0 {
			continue
		}

		for j := 0; j < counts[idWithFlows[i].DataID]; j++ {
			// 每次都要重新计算权重排序
			gidxes := ab.sortGroupIndex(groups)
			var has bool
			for k := 0; k < len(gidxes); k++ {
				if gset.Get(gidxes[k], idWithFlows[i].DataID) {
					continue
				}

				has = true
				groups[gidxes[k]] = append(groups[gidxes[k]], idWithFlows[i])
				gset.Set(gidxes[k], idWithFlows[i].DataID)
				break
			}

			// 如果实在每个服务实例都有的话 那还是给负载最小的分组
			if !has {
				groups[gidxes[0]] = append(groups[gidxes[0]], idWithFlows[i])
				gset.Set(gidxes[0], idWithFlows[i].DataID)
			}
		}
		counts[idWithFlows[i].DataID] = 0
	}

	// 流量为 0 的 dataid 进行特殊的标记处理
	zeroFlows := make(map[int]int)
	for i := len(idWithFlows) - 1; i >= 0; i-- {
		if idWithFlows[i].Flow != 0 {
			continue
		}
		zeroFlows[idWithFlows[i].DataID] = counts[idWithFlows[i].DataID]
		counts[idWithFlows[i].DataID] = 0
	}

	draft := Draft{
		TotalWeight: total,
		AvgWeight:   gaw,
		original:    items,
		zeroFlows:   zeroFlows,
	}

	for _, row := range groups {
		gsum := 0
		for _, g := range row {
			gsum += g.Flow
		}
		sort.Slice(row, func(i, j int) bool {
			return row[i].DataID < row[j].DataID
		})

		var percent float64
		if total > 0 {
			percent = float64(gsum) / float64(total)
		}

		// 计算分组百分比和权重
		draft.Groups = append(draft.Groups, FlowGroup{
			Group:   row,
			Weight:  gsum,
			Percent: percent,
		})
	}

	draft.Overflow = overflow

	var percents []float64
	for _, g := range draft.Groups {
		percents = append(percents, g.Percent)
	}
	sort.Float64s(percents)
	var ratio float64
	if len(percents) >= 2 && percents[0] > 0 {
		ratio = percents[len(percents)-1] / percents[0]
	}
	draft.MaxRatio = ratio

	// 确保分配出来的 groups 是有序的
	draft.SortGroups()

	// 调度结果指标打点
	autoBalancerBestOverflow.Set(draft.Overflow)
	autoBalancerMaxFlowRatio.Set(draft.MaxRatio)
	return draft
}

func NewOriginalDetailsBalanceElementsWithID(id int, details interface{}, count int) []define.IDer {
	els := make([]define.IDer, count)
	bases := NewOriginalIDBalanceElements(id, count)

	for i := range els {
		els[i] = &DetailsBalanceElement{
			BalanceElement: bases[i],
			Details:        details,
		}
	}

	return els
}

func NewOriginalIDBalanceElements(base int, repeat int) []*BalanceElement {
	els := make([]*BalanceElement, 0, repeat)
	for i := 0; i < repeat; i++ {
		els = append(els, NewIDBalanceElement(base))
	}

	return els
}
