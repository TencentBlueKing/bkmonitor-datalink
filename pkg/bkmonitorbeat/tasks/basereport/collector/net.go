// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"regexp"
	"time"

	"github.com/shirou/gopsutil/v3/net"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type NetReport struct {
	Interface []net.InterfaceStat         `json:"interface"`
	Stat      []Stat                      `json:"dev"`
	Netstat   SocketStatusCount           `json:"netstat"`
	Protocol  map[string]map[string]int64 `json:"protocolstat"`
}

type Stat struct {
	net.IOCountersStat
	NetInfo

	SpeedSent        uint64 `json:"speedSent"`        // speed of sent, bytes/second
	SpeedRecv        uint64 `json:"speedRecv"`        // speed of received, bytes/second
	SpeedPacketsSent uint64 `json:"speedPacketsSent"` // speed of packets sent, nr/second
	SpeedPacketsRecv uint64 `json:"speedPacketsRecv"` // speed of packets received, nr/second
}

type NetInfo struct {
	Errors     uint64 `json:"errors"`
	Dropped    uint64 `json:"dropped"`
	Fifo       uint64 `json:"overruns"`
	Carrier    uint64 `json:"carrier"`
	Collisions uint64 `json:"collisions"`
}

type NetFIFOQueue struct {
	queue [][]Stat
	max   int
}

// 初始化队列
func NewNetFIFOQueue(max int) *NetFIFOQueue {
	return &NetFIFOQueue{
		queue: make([][]Stat, 0, max),
		max:   max,
	}
}

// Push 方法，将一组 Stat 对象加入队列
func (f *NetFIFOQueue) Push(stats []Stat) {
	if len(f.queue) >= f.max {
		f.queue = f.queue[1:] // 如果队列已满，移除最早的一轮数据
	}
	f.queue = append(f.queue, stats) // 添加新的数据轮
}

// CheckMonotonicIncrease 方法，判断是否满足单调递增条件
func (f *NetFIFOQueue) CheckMonotonicIncrease() bool {
	// 如果队列长度小于 max，直接返回 false
	if len(f.queue) < f.max {
		return false
	}

	// 构建每个网卡在各轮次中的数据序列
	networkStats := make(map[string][]Stat)
	for _, round := range f.queue {
		for _, stat := range round {
			networkStats[stat.Name] = append(networkStats[stat.Name], stat)
		}
	}

	// 检查每个网卡的属性是否单调递增
	for _, stats := range networkStats {
		if !f.isNetworkStatsMonotonic(stats) {
			return false
		}
	}

	return true
}

// isNetworkStatsMonotonic 检查单个网卡的统计数据是否单调递增
func (f *NetFIFOQueue) isNetworkStatsMonotonic(stats []Stat) bool {
	if len(stats) < 2 {
		return true // 数据不足，认为是单调的
	}

	for i := 1; i < len(stats); i++ {
		prev := stats[i-1]
		curr := stats[i]

		if curr.BytesSent <= prev.BytesSent ||
			curr.BytesRecv <= prev.BytesRecv ||
			curr.PacketsSent <= prev.PacketsSent ||
			curr.PacketsRecv <= prev.PacketsRecv {
			return false
		}
	}

	return true
}

var (
	virtualInterfaceSet = common.NewSet()
	netStatFIFOQueue    = NewNetFIFOQueue(5)
	lastNetStatMap      map[string]net.IOCountersStat
	errCount            int
)

var lastStatTime time.Time

var lastProtoCounters = make(map[string]map[string]int64)

func getProtocolStats() (map[string]map[string]int64, error) {
	protos := []string{"udp", "tcp"}
	protoCounters, err := ProtoCounters(protos) // only implement on Linux
	if err != nil {
		return nil, err
	}

	ret := make(map[string]map[string]int64)
	origin := make(map[string]map[string]int64)
	for _, pc := range protoCounters {
		counter := make(map[string]int64)
		for key, value := range pc.Stats {
			normalizeKey := common.FirstCharToLower(key)

			// 记录原始数据值
			if _, ok := origin[pc.Protocol]; !ok {
				origin[pc.Protocol] = make(map[string]int64)
			}
			origin[pc.Protocol][normalizeKey] = value

			lastV := value // 如果取不到上一个周期的话置 0 否则可能会取到一个非常大的数值
			if lastCounter, ok := lastProtoCounters[pc.Protocol]; ok {
				lastV = lastCounter[normalizeKey]
			}
			counter[normalizeKey] = value - lastV // 计算两个周期差值
		}
		ret[pc.Protocol] = counter
	}

	lastProtoCounters = origin
	return ret, nil
}

func getStatByIOCounterStat(stat []net.IOCountersStat) ([]Stat, error) {
	// get netinfo form 'dev' in linux,
	netinfo, err := GetNetInfoFromDev()
	if err != nil {
		logger.Errorf("get netinfo from dev err :%s", err)
		return nil, err
	}
	s := make([]Stat, 0)
	for _, value := range stat {
		netInfoItem, ok := netinfo[value.Name]
		if !ok {
			s = append(s, Stat{IOCountersStat: value})
			continue
		}
		// 计算网卡信息总和
		netInfoItem.Errors += value.Errin
		netInfoItem.Errors += value.Errout
		netInfoItem.Dropped += value.Dropin
		netInfoItem.Dropped += value.Dropout
		netInfoItem.Fifo += value.Fifoin
		netInfoItem.Fifo += value.Fifoout
		s = append(s, Stat{IOCountersStat: value, NetInfo: netInfoItem})
	}
	return s, nil
}

func resetNetSpeedRate(once *NetReport) {
	for i := range once.Stat {
		once.Stat[i].SpeedRecv = 0
		once.Stat[i].SpeedSent = 0
		once.Stat[i].SpeedPacketsRecv = 0
		once.Stat[i].SpeedPacketsSent = 0
	}
}

func updateNetSpeed(once *NetReport, interval uint64) {
	// 速率计算需要保证数据单调递增，不能出现倒流
	netStatFIFOQueue.Push(once.Stat)
	hasBackflow := false

	if len(lastNetStatMap) > 0 {
		for i := range once.Stat {
			// net devices maybe changed, should check net name
			if val, ok := lastNetStatMap[once.Stat[i].Name]; ok {
				// 当任何一次出现倒流认为该次错误
				if once.Stat[i].BytesSent < val.BytesSent ||
					once.Stat[i].BytesRecv < val.BytesRecv ||
					once.Stat[i].PacketsRecv < val.PacketsRecv ||
					once.Stat[i].PacketsSent < val.PacketsSent {
					hasBackflow = true
					break
				}
				once.Stat[i].SpeedRecv = (CounterDiff(once.Stat[i].BytesRecv, val.BytesRecv)) / interval
				once.Stat[i].SpeedSent = (CounterDiff(once.Stat[i].BytesSent, val.BytesSent)) / interval
				once.Stat[i].SpeedPacketsRecv = (CounterDiff(once.Stat[i].PacketsRecv, val.PacketsRecv)) / interval
				once.Stat[i].SpeedPacketsSent = (CounterDiff(once.Stat[i].PacketsSent, val.PacketsSent)) / interval
			}
		}
	}

	// 检查是否有倒流且FIFO队列不满足单调递增
	if hasBackflow && !netStatFIFOQueue.CheckMonotonicIncrease() {
		// 速率与包数全部置0,错误次数加1,且保留上次lastNetStatMap
		errCount++
		logger.Errorf("The NIC traffic reverts for %d times", errCount)
		resetNetSpeedRate(once)
		return
	}

	// 处理错误恢复场景
	if errCount > 0 {
		// 出现过错误的首次恢复,覆盖lastNetStatMap
		lastNetStatMap = make(map[string]net.IOCountersStat)
		for _, val := range once.Stat {
			lastNetStatMap[val.Name] = val.IOCountersStat
		}
		// 该次数据不用来计算速率与包数,可能错误周期较长，导致算出的值不准确，不如扔掉
		resetNetSpeedRate(once)
		errCount = 0
		logger.Debugf("Nic backflow recovery")
		return
	}

	// 正常情况下更新lastNetStatMap
	lastNetStatMap = make(map[string]net.IOCountersStat)
	for _, val := range once.Stat {
		lastNetStatMap[val.Name] = val.IOCountersStat
	}
}

func updateMaxNetStatMap(stats []Stat, maxNetStatMap map[string]Stat) {
	for _, currentStat := range stats {
		// net devices maybe changed, should check net name
		if maxStat, ok := maxNetStatMap[currentStat.Name]; ok {
			currentMax := common.MaxUInt(currentStat.SpeedRecv, currentStat.SpeedSent)
			max := common.MaxUInt(maxStat.SpeedRecv, maxStat.SpeedSent)
			if currentMax > max {
				maxNetStatMap[maxStat.Name] = currentStat
				logger.Infof("update max net dev %s io, %d > %d", maxStat.Name, currentMax, max)
			}
		} else {
			maxNetStatMap[currentStat.Name] = currentStat
			logger.Infof("first net dev %s", currentStat.Name)
		}
	}
}

func GetNetInfo(config configs.NetConfig) (*NetReport, error) {
	var report NetReport
	var err error

	// 采样多次，取带宽最大值
	maxNetStatMap := make(map[string]Stat)

	if config.SkipVirtualInterface {
		// 初始化虚拟网卡列表，用于后面过滤
		err = initVirtualInterfaceSet()
		if err != nil {
			logger.Warnf("get virtual interface set failed,error:%s", err)
		}
	}

	count := config.StatTimes
	ticker := time.NewTicker(config.StatPeriod)
	defer ticker.Stop()
	for {
		var once NetReport

		now := time.Now()
		stat, err := net.IOCounters(true)
		if err != nil {
			logger.Errorf("get net IOCounters fail for->[%s]", err)
			return nil, err
		}
		stat = FilterNetIOStats(stat, config)

		once.Stat, err = getStatByIOCounterStat(stat)
		if err != nil {
			return nil, err
		}
		interval := uint64(now.Sub(lastStatTime).Seconds())
		if interval == 0 {
			// in case devide 0
			interval = 1
		}
		logger.Debugf("net interval=%d", interval)
		lastStatTime = now
		updateNetSpeed(&once, interval)
		// select max net io report
		updateMaxNetStatMap(once.Stat, maxNetStatMap)
		logger.Debugf("There are %d rounds left to end this collection:net get io %v", count, once.Stat)

		count--
		if count <= 0 {
			break
		}

		select {
		case <-ticker.C:
		}
	}

	for _, stat := range maxNetStatMap {
		report.Stat = append(report.Stat, stat)
	}

	// collect once

	report.Interface, err = net.Interfaces()
	if err != nil {
		logger.Error("get net interfaces fail")
		return nil, err
	}

	report.Interface = FilterNetInterfaceStats(report.Interface, config)

	// netstat
	// ignore error, netstat will be empty when error occur
	report.Netstat, _ = GetTcp4SocketStatusCount()

	protocolStats, err := getProtocolStats()
	if err == nil {
		report.Protocol = protocolStats
	}

	return &report, nil
}

func isVirtualInterface(name string) bool {
	return virtualInterfaceSet.Exist(name)
}

func CheckInStringList(name string, list []*regexp.Regexp) bool {
	for _, item := range list {
		// 如果正则匹配为非空，那么命中
		if result := item.FindStringIndex(name); result != nil {
			return true
		}
	}
	return false
}

// 根据黑白名单检查是否对应的数据应该上报，返回true则应上报，false则不应上报
func CheckBlackWhiteList(name string, whiteList []*regexp.Regexp, blackList []*regexp.Regexp) bool {
	// 优先白名单，如果未配置白名单则使用黑名单
	if len(whiteList) != 0 {
		// 若存在于白名单中，则上报
		if CheckInStringList(name, whiteList) {
			return true
		}
		return false
	}
	// 白名单未配置，则检查黑名单
	if len(blackList) != 0 {
		// 存在于黑名单中，则不上报
		if CheckInStringList(name, blackList) {
			return false
		}
		return true
	}
	// 黑白名单都没配置，则全量上报
	return true
}

func CheckInSimpleList(name string, list []*regexp.Regexp) bool {
	for _, item := range list {
		if item.MatchString(name) {
			return true
		}
	}
	return false
}

func FilterNetIOStats(ioCounterStat []net.IOCountersStat, config configs.NetConfig) []net.IOCountersStat {
	// 用黑白名单过滤net接口
	filteredList := make([]net.IOCountersStat, 0, len(ioCounterStat))
	for _, stat := range ioCounterStat {

		// 配置了强制上报的优先处理
		if CheckInSimpleList(stat.Name, config.ForceReportList) {
			logger.Debugf("interface->[%s] is in force report list, will report it.", stat.Name)
			filteredList = append(filteredList, stat)
			continue
		}

		// 黑白名单过滤
		if !CheckBlackWhiteList(stat.Name, config.InterfaceWhiteList, config.InterfaceBlackList) {
			continue
		}
		// 虚拟网卡过滤, 但是前提需要不在白名单中才会进行虚拟网卡检查
		// 如果配置了白名单，则跳过虚拟网卡的检查，必然会加入到上报队列中
		if !CheckInStringList(stat.Name, config.InterfaceWhiteList) && config.SkipVirtualInterface && isVirtualInterface(stat.Name) {
			continue
		}
		filteredList = append(filteredList, stat)
	}
	return filteredList
}

func FilterNetInterfaceStats(interfaceStats []net.InterfaceStat, config configs.NetConfig) []net.InterfaceStat {
	// 用黑白名单过滤net接口
	filteredInterfaceList := make([]net.InterfaceStat, 0, len(interfaceStats))
	for _, inter := range interfaceStats {
		// 配置了强制上报的优先处理
		if CheckInSimpleList(inter.Name, config.ForceReportList) {
			logger.Debugf("interface->[%s] is in force report list, will report it.", inter.Name)
			filteredInterfaceList = append(filteredInterfaceList, inter)
			continue
		}
		// 过滤黑白名单
		if !CheckBlackWhiteList(inter.Name, config.InterfaceWhiteList, config.InterfaceBlackList) {
			continue
		}
		// 虚拟网卡过滤, 但是前提需要不在白名单中才会进行虚拟网卡检查
		// 如果配置了白名单，则跳过虚拟网卡的检查，必然会加入到上报队列中
		if !CheckInStringList(inter.Name, config.InterfaceWhiteList) && config.SkipVirtualInterface && isVirtualInterface(inter.Name) {
			logger.Debugf("interface->[%s] not match white list and is virtual, will jump it.", inter.Name)
			continue
		}
		filteredInterfaceList = append(filteredInterfaceList, inter)
	}
	return filteredInterfaceList
}
