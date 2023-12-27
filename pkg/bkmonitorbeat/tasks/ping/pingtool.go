// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ping

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/tatsushid/go-fastping"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Tool ping接口
type Tool interface {
	// 单次全ping
	Ping(doFunc DoFunc) error
}

// Info ping的信息记录表
type Info struct {
	Name       string
	Type       string
	Addr       net.Addr
	RecvCount  int
	TotalCount int
	MaxRTT     float64
	MinRTT     float64
	TotalRTT   float64
	Code       int
}

// BatchPingTool 批量Ping
type BatchPingTool struct {
	ctx          context.Context
	targetList   []Target
	maxRTT       time.Duration
	totalCount   int
	batchSize    int
	size         int
	targetIPType configs.IPType
	dnsCheckMode configs.CheckMode
	s            tasks.Semaphore
}

// Target 接口，任何实现该接口的数据都可传入
type Target interface {
	GetTarget() string
	GetTargetType() string
}

// DoFunc 处理数据的接口方法
type DoFunc func(resMap map[string]map[string]*Info, wg *sync.WaitGroup)

// NewBatchPingTool :
var NewBatchPingTool = func(
	ctx context.Context, targetList []Target, totalNum int, maxRTT string, size int, batchSize int,
	targetIPType configs.IPType, dnsCheckMode configs.CheckMode, s tasks.Semaphore,
) (Tool, error) {
	var err error
	pingTool := new(BatchPingTool)
	pingTool.ctx = ctx
	pingTool.targetList = targetList
	pingTool.totalCount = totalNum
	pingTool.batchSize = batchSize
	pingTool.size = size
	pingTool.targetIPType = targetIPType
	pingTool.dnsCheckMode = dnsCheckMode
	pingTool.s = s

	pingTool.maxRTT, err = time.ParseDuration(maxRTT)
	if err != nil {
		logger.Errorf("init max_rtt failed,err:%v", err)
		return nil, err
	}
	return pingTool, nil

}

// Ping 开始分批ping
func (t *BatchPingTool) Ping(doFunc DoFunc) error {
	// 分割url列表，批次处理
	lists := [][]Target{
		t.targetList,
	}
	// 如果配置了批次值，则使用分批次ping的策略
	if t.batchSize != 0 {
		lists = t.divideLists(t.targetList, t.batchSize)
	}

	wg := new(sync.WaitGroup)
	for _, list := range lists {
		select {
		case <-t.ctx.Done():
			{
				logger.Info("get ctx done")
				return nil
			}
		default:
			{
				logger.Debugf("length of single ping list:%v,ping start", len(list))
				// 按照给定的ip列表，初始化结果map
				infoList := t.initInfoList(list)
				// 执行实际的ping方法
				resMap := DoPing(t.ctx, infoList, t)
				logger.Debugf("do ping return,get resMap:%v,length:%v", resMap, len(resMap))
				// 获取并发限制信号量
				err := t.s.Acquire(context.Background(), 1)
				if err != nil {
					logger.Errorf("Semaphore Acquire failed for task ping task id: %d")
					return err
				}
				// 处理ping任务的结果，使用异步以优化分批次处理时的效率
				wg.Add(1)
				go doFunc(resMap, wg)
			}
		}
	}
	// 等待所有任务结束
	wg.Wait()
	return nil
}

func (t *BatchPingTool) getIP(target Target) ([]net.IP, error) {
	// 类型为域名
	if target.GetTargetType() == "domain" {
		ips, err := tasks.LookupIP(context.Background(), t.targetIPType, target.GetTarget())
		if err != nil {
			return nil, &define.BeaterUpMetricErr{Code: define.BeatPingDNSResolveOuterError, Message: err.Error()}
		}
		// 检测全部模式返回所有ip列表
		if t.dnsCheckMode == configs.CheckModeAll {
			return ips, nil
		}
		// 非检测全部模式返回第一个
		if len(ips) > 0 {
			return ips[:1], nil
		}
		return nil, nil
	}
	// 类型为ip
	ip := net.ParseIP(target.GetTarget())
	if ip == nil {
		return nil, &define.BeaterUpMetricErr{Code: define.BeatPingInvalidIPOuterError, Message: "invalid ip"}
	}
	return []net.IP{ip}, nil
}

// initInfoList 将string类型的urls转换为ipaddr类型,并生成记录结果的map，key为目标-ip，值为对应测试信息
func (t *BatchPingTool) initInfoList(list []Target) map[string]map[string]*Info {
	addrMap := make(map[string]map[string]*Info)
	for _, v := range list {
		// 按目标填入map
		addrMap[v.GetTarget()] = make(map[string]*Info)
		// 解析地址对应ip列表
		ips, err := t.getIP(v)
		if err != nil {
			logger.Errorf("getIP failed: %v config: %+v", err, v)
			var upErr *define.BeaterUpMetricErr
			var upCode int
			if errors.As(err, &upErr) {
				upCode = upErr.Code
			} else {
				upCode = define.BeatErrInternalErr
			}
			info := new(Info)
			info.Name = v.GetTarget()
			info.Type = v.GetTargetType()
			info.Code = upCode
			// 目标地址解析不到主机IP，则置空，写入异常结论
			addrMap[v.GetTarget()][""] = info
			continue
		}
		// 循环处理ip列表生成拨测信息并放到目标对应的map
		for _, ip := range ips {
			info := new(Info)
			info.Name = v.GetTarget()
			info.Type = v.GetTargetType()
			info.Addr = &net.IPAddr{IP: ip}
			info.RecvCount = 0
			info.TotalCount = t.totalCount
			// 按ip填入对应目标下的map
			addrMap[v.GetTarget()][ip.String()] = info
		}
	}
	return addrMap
}

// divideLists 按照batch生成批次列表
func (t *BatchPingTool) divideLists(totalList []Target, batchSize int) [][]Target {
	lists := make([][]Target, 0)
	divideTimes := len(totalList) / batchSize
	logger.Debugf("length of total:%v,batchSize:%v,ip list should be divided into %v parts", len(totalList), batchSize, divideTimes)
	count := 0
	for count <= divideTimes {
		if count == divideTimes {
			tempList := totalList[count*batchSize:]
			lists = append(lists, tempList)
		} else {
			tempList := totalList[count*batchSize : (count+1)*batchSize]
			lists = append(lists, tempList)
		}
		count++
	}
	return lists
}

// DoPing 实际进行ping的方法,使用fastping包
var DoPing = func(ctx context.Context, resMap map[string]map[string]*Info, t *BatchPingTool) map[string]map[string]*Info {
	// 获取ping对象
	p := fastping.NewPinger()
	nameMapping := make(map[string]map[string]struct{})
	ipsCount := 0
	for target, infoMap := range resMap {
		for _, info := range infoMap {
			ra, ok := info.Addr.(*net.IPAddr)
			if ok {
				p.AddIPAddr(ra)
				// 域名解析成功的，增加映射标志
				_, ok := nameMapping[ra.IP.String()]
				if !ok {
					nameMapping[ra.IP.String()] = map[string]struct{}{target: {}}
				} else {
					nameMapping[ra.IP.String()][target] = struct{}{}
				}
				ipsCount++
			}
		}
	}

	totalCount := 0
	jobCtx, jobCancel := context.WithCancel(ctx)
	// 回调函数，当收到返回值时调用
	p.OnRecv = func(addr *net.IPAddr, rtt time.Duration) {
		// 根据ip反向找到key，这里这么写是因为回调函数只有ip
		ipStr := addr.IP.String()
		targets, ok := nameMapping[ipStr]
		if !ok {
			logger.Warnf("get unexpected ip:%s", addr.IP.String())
			return
		}

		for target := range targets {
			pingInfo, ok := resMap[target][ipStr]
			if ok {
				logger.Debugf("target:%s,received icmp package", target)
				pingInfo.RecvCount++
				// 取得毫秒级数据
				rttMillSecond := rtt.Seconds() * 1000
				// 每次都更新rtt最大值
				if rttMillSecond > pingInfo.MaxRTT {
					pingInfo.MaxRTT = rttMillSecond
				}
				// 第一次接收到包的时候，将其值设置为最小值
				if pingInfo.RecvCount == 1 {
					pingInfo.MinRTT = rttMillSecond
				}
				if pingInfo.MinRTT > rttMillSecond {
					pingInfo.MinRTT = rttMillSecond
				}
				pingInfo.TotalRTT += rttMillSecond

				resMap[target][ipStr] = pingInfo
				// 计数
				totalCount++
				// 全部收到响应关闭
				if totalCount == ipsCount {
					jobCancel()
					totalCount = 0
				}
			} else {
				logger.Errorf("missing inited pingInfo,target:%s", target)
			}
		}
	}

	p.OnIdle = func() {
		jobCancel()
		totalCount = 0
	}

	p.Size = t.size
	p.MaxRTT = t.maxRTT
	p.Debug = false
	_, err := p.Source("")
	if err != nil {
		logger.Errorf("pingtool source failed,error:%v", err)
		return resMap
	}
	// 循环channel，当循环结束时调用，缓冲为1是为了处理外部线程退出而内部没有退出的情况
	loopChannel := make(chan int, 1)
	// 周期执行ping的线程
	go func() {
		defer func() {
			logger.Info("loop end,send signal")
			loopChannel <- 1
		}()
		for i := 0; i < t.totalCount; i++ {
			logger.Debugf("ping start,times:%d", i)
			p.RunLoop()
			select {
			case <-jobCtx.Done():
				p.Stop()
				jobCtx, jobCancel = context.WithCancel(ctx)
			case <-ctx.Done():
				p.Stop()
				return
			}
			<-p.Done()

		}
	}()

	// 控制任务主流程,等待loop结束，或ctx结束
	select {
	case <-ctx.Done():
		logger.Infof("get ctx done,reason:%v", ctx.Err())
		break
	case <-loopChannel:
		logger.Info("get loop end signal,return result")
		break
	}

	return resMap
}
