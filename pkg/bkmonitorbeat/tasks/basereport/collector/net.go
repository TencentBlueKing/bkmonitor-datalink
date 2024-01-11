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

var (
	virtualInterfaceSet = common.NewSet()
	lastNetStatMap      map[string]net.IOCountersStat
	lastUdpStat         net.ProtoCountersStat
	lastStatTime        time.Time
)

func GetNetInfo(config configs.NetConfig) (*NetReport, error) {
	var report NetReport
	var err error

	// 采样多次，取带宽最大值
	maxNetStatMap := make(map[string]Stat)

	// 初始化虚拟网卡列表，用于后面过滤
	if config.SkipVirtualInterface {
		if err = InitVirtualInterfaceSet(); err != nil {
			logger.Warnf("init virtual interface failed, err: %s", err)
		}
	}

	count := config.StatTimes
	ticker := time.NewTicker(config.StatPeriod)
	defer ticker.Stop()

	for {
		logger.Debug("collect net io")
		var once NetReport

		now := time.Now()
		stat, err := net.IOCounters(true)
		if err != nil {
			return nil, err
		}

		stat = filterNetIOStats(stat, config)
		once.Stat, err = sumIOCounterStats(stat)
		if err != nil {
			return nil, err
		}
		logger.Debugf("net get io %+v", once.Stat)

		interval := uint64(now.Sub(lastStatTime).Seconds())
		if interval == 0 {
			interval = 1
		}
		lastStatTime = now

		updateNetSpeed(&once, interval)
		updateMaxNetStatMap(once.Stat, maxNetStatMap)

		count--
		if count <= 0 {
			break
		}

		select {
		case <-ticker.C:
		}
	}

	// 上报数据取最大值
	for _, stat := range maxNetStatMap {
		report.Stat = append(report.Stat, stat)
	}

	report.Interface, err = net.Interfaces()
	if err != nil {
		return nil, err
	}
	report.Interface = filterNetInterfaceStats(report.Interface, config)

	report.Protocol, err = getProtocolStats()
	if err != nil {
		logger.Errorf("failed to get protocol stats: %v", err)
	}

	report.Netstat, err = GetTcp4SocketStatusCount()
	if err != nil {
		logger.Errorf("failed to get tcp4 socket stats: %v", err)
	}

	return &report, nil
}

// getProtocolStats 获取 procstats 数据 目前仅采集 udp 协议
func getProtocolStats() (map[string]map[string]int64, error) {
	protocols := []string{"udp"}
	stats, err := ProtoCounters(protocols)
	if err != nil {
		return nil, err
	}

	stat := stats[0]
	var udpStat net.ProtoCountersStat
	udpStat.Protocol = stat.Protocol
	udpStat.Stats = make(map[string]int64)

	// 首字母转小写
	for key, value := range stat.Stats {
		lowerKey := common.FirstCharToLower(key)
		udpStat.Stats[lowerKey] = value - lastUdpStat.Stats[key]
	}

	lastUdpStat = stats[0]
	ret := make(map[string]map[string]int64)
	ret[udpStat.Protocol] = udpStat.Stats
	return ret, nil
}

func sumIOCounterStats(stats []net.IOCountersStat) ([]Stat, error) {
	ret := make([]Stat, 0)
	for _, stat := range stats {
		var netInfo NetInfo
		netInfo.Errors += stat.Errin
		netInfo.Errors += stat.Errout
		netInfo.Dropped += stat.Dropin
		netInfo.Dropped += stat.Dropout
		netInfo.Fifo += stat.Fifoin
		netInfo.Fifo += stat.Fifoout
		ret = append(ret, Stat{IOCountersStat: stat, NetInfo: netInfo})
	}
	return ret, nil
}

// updateNetSpeed 更新 netspeed 最新状态
func updateNetSpeed(once *NetReport, interval uint64) {
	if len(lastNetStatMap) > 0 {
		for i := range once.Stat {
			val, ok := lastNetStatMap[once.Stat[i].Name]
			if !ok {
				continue
			}

			once.Stat[i].SpeedRecv = (calcDelta(once.Stat[i].BytesRecv, val.BytesRecv)) / interval
			once.Stat[i].SpeedSent = (calcDelta(once.Stat[i].BytesSent, val.BytesSent)) / interval
			once.Stat[i].SpeedPacketsRecv = (calcDelta(once.Stat[i].PacketsRecv, val.PacketsRecv)) / interval
			once.Stat[i].SpeedPacketsSent = (calcDelta(once.Stat[i].PacketsSent, val.PacketsSent)) / interval
		}
	}

	lastNetStatMap = make(map[string]net.IOCountersStat)
	for _, val := range once.Stat {
		lastNetStatMap[val.Name] = val.IOCountersStat
	}
}

// updateMaxNetStatMap 取 max netspeed
func updateMaxNetStatMap(stats []Stat, maxNetStatMap map[string]Stat) {
	for _, currentStat := range stats {
		maxStat, ok := maxNetStatMap[currentStat.Name]
		if !ok {
			maxNetStatMap[currentStat.Name] = currentStat
			logger.Debugf("find first net dev(%s)", currentStat.Name)
			continue
		}

		currMax := common.MaxUInt(currentStat.SpeedRecv, currentStat.SpeedSent)
		prevMax := common.MaxUInt(maxStat.SpeedRecv, maxStat.SpeedSent)
		if currMax > prevMax {
			maxNetStatMap[maxStat.Name] = currentStat
			logger.Debugf("update max net dev(%s) io, currMax=%d, prevMax=%d", maxStat.Name, currMax, prevMax)
		}
	}
}

func isVirtualInterface(name string) bool {
	return virtualInterfaceSet.Exist(name)
}

func checkInStringList(name string, list []*regexp.Regexp) bool {
	for _, item := range list {
		// 如果正则匹配为非空，那么命中
		if result := item.FindStringIndex(name); result != nil {
			return true
		}
	}
	return false
}

func checkInSimpleList(name string, list []*regexp.Regexp) bool {
	for _, item := range list {
		if item.MatchString(name) {
			return true
		}
	}
	return false
}

// checkBlackWhiteList 根据黑白名单检查是否对应的数据应该上报
// 返回 true 则应上报，false 则不应上报
func checkBlackWhiteList(name string, whiteList []*regexp.Regexp, blackList []*regexp.Regexp) bool {
	// 优先白名单，如果未配置白名单则使用黑名单
	if len(whiteList) != 0 {
		// 若存在于白名单中，则上报
		if checkInStringList(name, whiteList) {
			return true
		}
		return false
	}
	// 白名单未配置，则检查黑名单
	if len(blackList) != 0 {
		// 存在于黑名单中，则不上报
		if checkInStringList(name, blackList) {
			return false
		}
		return true
	}
	// 黑白名单都没配置，则全量上报
	return true
}

// filterNetIOStats 用黑白名单过滤 net 接口
func filterNetIOStats(ioCounterStat []net.IOCountersStat, config configs.NetConfig) []net.IOCountersStat {
	stats := make([]net.IOCountersStat, 0, len(ioCounterStat))
	for _, stat := range ioCounterStat {
		// 配置了强制上报的优先处理
		if checkInSimpleList(stat.Name, config.ForceReportList) {
			logger.Debugf("interface(%s) is in force report list, will report", stat.Name)
			stats = append(stats, stat)
			continue
		}

		// 黑白名单过滤
		if !checkBlackWhiteList(stat.Name, config.InterfaceWhiteList, config.InterfaceBlackList) {
			continue
		}
		// 虚拟网卡过滤, 但是前提需要不在白名单中才会进行虚拟网卡检查
		// 如果配置了白名单，则跳过虚拟网卡的检查，必然会加入到上报队列中
		if !checkInStringList(stat.Name, config.InterfaceWhiteList) && config.SkipVirtualInterface && isVirtualInterface(stat.Name) {
			continue
		}
		stats = append(stats, stat)
	}
	return stats

}

// filterNetInterfaceStats 用黑白名单过滤 net 接口
func filterNetInterfaceStats(interfaceStats []net.InterfaceStat, config configs.NetConfig) []net.InterfaceStat {
	stats := make([]net.InterfaceStat, 0, len(interfaceStats))
	for _, stat := range interfaceStats {
		// 配置了强制上报的优先处理
		if checkInSimpleList(stat.Name, config.ForceReportList) {
			logger.Debugf("interface(%s) is in force report list, will report", stat.Name)
			stats = append(stats, stat)
			continue
		}
		// 过滤黑白名单
		if !checkBlackWhiteList(stat.Name, config.InterfaceWhiteList, config.InterfaceBlackList) {
			continue
		}
		// 虚拟网卡过滤, 但是前提需要不在白名单中才会进行虚拟网卡检查
		// 如果配置了白名单，则跳过虚拟网卡的检查，必然会加入到上报队列中
		if !checkInStringList(stat.Name, config.InterfaceWhiteList) && config.SkipVirtualInterface && isVirtualInterface(stat.Name) {
			logger.Debugf("interface(%s) not match white list and is virtual, will skip", stat.Name)
			continue
		}
		stats = append(stats, stat)
	}
	return stats

}
