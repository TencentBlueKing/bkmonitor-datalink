// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pingserver

import (
	"math"
	"net"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/notifier"
)

var (
	rollPingRTT   = []int{3, 10} // 滚动 ping 两轮，每一轮的 rtt 配置
	globalRecords = define.NewRecordQueue(define.PushModeGuarantee)
)

// Records 返回 Receiver 全局消息管道
func Records() <-chan *define.Record {
	return globalRecords.Get()
}

type Pingserver struct {
	config   *Config
	round    int
	done     chan struct{}
	notifier *notifier.Notifier
	patterns []string

	createDetector func(addrs []*net.IPAddr, times int, timeout time.Duration) Detector
}

func New(conf *confengine.Config) (*Pingserver, error) {
	ps, err := newPingserver(conf)
	if err != nil {
		return nil, err
	}

	if ps.config.Main.AutoReload {
		logger.Info("pingserver: start to autoreload...")
		ps.notifier = notifier.New(time.Minute, ps.patterns...)
	}

	return ps, nil
}

func newPingserver(conf *confengine.Config) (*Pingserver, error) {
	patterns, config, err := LoadConfig(conf)
	if err != nil {
		return nil, err
	}
	DefaultMetricMonitor.SetTargetsCount(config.Sub.DataId, len(config.Sub.Targets))

	logger.Infof("pingserver found %d targets config", len(config.Sub.Targets))
	ps := &Pingserver{
		config:         config,
		done:           make(chan struct{}, 1),
		patterns:       patterns,
		createDetector: newDetector,
	}
	return ps, nil
}

func (ps *Pingserver) Start() error {
	logger.Info("pingserver start working...")
	go func() {
		if ps.notifier != nil {
			for range ps.notifier.Ch() {
				logger.Info("pingserver receive notifier signal")
				beat.ReloadChan <- true
			}
		}
	}()

	go ps.start()
	return nil
}

func (ps *Pingserver) start() {
	ticker := time.NewTicker(ps.config.Sub.Period)
	defer ticker.Stop()

	if ps.round <= 0 {
		ps.doPing(time.Now())
	}

	for {
		select {
		case t := <-ticker.C:
			ps.doPing(t)

		case <-ps.done:
			return
		}
	}
}

func (ps *Pingserver) doPing(t time.Time) {
	timestamp := t.UnixMilli()

	addrs := ps.config.Sub.Addrs()
	ps.batchPing(addrs, timestamp)
	ps.round++
	logger.Debugf("ping addrs count=%d, round=%d", len(addrs), ps.round)

	DefaultMetricMonitor.IncPingCounter(ps.config.Sub.DataId)
	DefaultMetricMonitor.ObservePingDuration(time.Now(), ps.config.Sub.DataId)
}

func (ps *Pingserver) Stop() {
	close(ps.done)

	if ps.notifier != nil {
		ps.notifier.Close()
	}
}

func (ps *Pingserver) Reload(conf *confengine.Config) error {
	newPs, err := newPingserver(conf)
	if err != nil {
		return err
	}

	if ps.notifier != nil {
		ps.notifier.SetPattern(newPs.patterns...)
	}
	ps.config = newPs.config

	return nil
}

func sum(nums []int) int {
	total := 0
	for _, value := range nums {
		total += value
	}
	return total
}

// batchPing 分批 ping
// 将 IP 分批，并均匀的打散在固定时间内，采用多个 goroutine 执行，打散的时间来源 (单位时间 — 滚动 ping 执行的时间)
// 比如单元时间是 1 分钟，即 60s，滚动 ping 一次，rtt 分别为 3，10，那么一次滚动 ping 耗时 39 秒，那么剩余的 21 秒可以用来打散
func (ps *Pingserver) batchPing(addrs []*net.IPAddr, timestamp int64) {
	var batch int
	var wg sync.WaitGroup

	each := math.Ceil(float64(len(addrs)) / float64(ps.config.Sub.MaxBatchSize))
	remained := int64(ps.config.Sub.Period/1e9) - int64(ps.config.Sub.Times*sum(rollPingRTT)) - 1
	if remained < 0 {
		remained = 0
	}
	sleepInterval := float64(remained) / each

	for batch*ps.config.Sub.MaxBatchSize < len(addrs) {
		left := batch * ps.config.Sub.MaxBatchSize
		right := left + ps.config.Sub.MaxBatchSize
		if right > len(addrs) {
			right = len(addrs)
		}

		wg.Add(1)
		go func(ips []*net.IPAddr, i int) {
			time.Sleep(time.Duration(int64(float64(i)*sleepInterval*1000)) * time.Millisecond)
			ps.rollPing(ips, timestamp)
			DefaultMetricMonitor.IncRollPingCounter(ps.config.Sub.DataId)
			wg.Done()
		}(addrs[left:right], batch)

		batch++
	}
	wg.Wait()
}

// rollPing 滚动 ping
// 单位时间内 一共两轮，每轮 ping 3 次，第一轮 rtt 为 3，第二轮 rtt 为 10，由这个配置决定 rollPingRTT
func (ps *Pingserver) rollPing(addrs []*net.IPAddr, timestamp int64) {
	for idx, rtt := range rollPingRTT {
		pp := ps.createDetector(addrs, ps.config.Sub.Times, time.Duration(rtt)*time.Second)
		pp.Do()

		results := pp.Result()
		if idx == len(rollPingRTT)-1 {
			ps.push(results, timestamp)
			continue
		}

		toSendResults := make(map[string]*Response)
		toContinueAddr := make([]*net.IPAddr, 0)
		for addr, resp := range results {
			if resp.RecvCount > 0 {
				toSendResults[addr] = resp
			} else {
				toContinueAddr = append(toContinueAddr, resp.Addr)
			}
		}
		ps.push(toSendResults, timestamp)

		if len(toContinueAddr) == 0 {
			break
		}
		addrs = toContinueAddr
	}
}

func (ps *Pingserver) push(results map[string]*Response, timestamp int64) {
	for _, resp := range results {
		targetIP := resp.Addr.IP.String()
		cloudId := ps.config.Sub.Server.CloudID
		addr := FormatTarget(targetIP, cloudId)

		bizId, ok := ps.config.Sub.GetBizId(addr)
		if !ok {
			DefaultMetricMonitor.IncDroppedCounter(ps.config.Sub.DataId)
			continue
		}

		data := make(map[string]any)
		data["timestamp"] = timestamp
		data["target"] = addr

		data["metrics"] = map[string]float64{
			"max_rtt":      0,
			"min_rtt":      0,
			"avg_rtt":      0,
			"loss_percent": 1.0,
		}

		if resp.RecvCount > 0 {
			data["metrics"] = map[string]float64{
				"max_rtt":      resp.MaxRtt.Seconds() * 1000,
				"min_rtt":      resp.MinRtt.Seconds() * 1000,
				"avg_rtt":      resp.TotalRtt.Seconds() * 1000 / float64(resp.RecvCount),
				"loss_percent": float64(ps.config.Sub.Times-resp.RecvCount) / float64(ps.config.Sub.Times),
			}
		}

		data["dimension"] = map[string]string{
			"bk_biz_id":          bizId,
			"bk_target_ip":       targetIP,
			"bk_target_cloud_id": cloudId,
			"bk_host_id":         ps.config.Sub.Server.HostID,
			"ip":                 ps.config.Sub.Server.Ip,
			"bk_cloud_id":        ps.config.Sub.Server.CloudID,
		}

		globalRecords.Push(&define.Record{
			RequestType: define.RequestICMP,
			RecordType:  define.RecordPingserver,
			Token: define.Token{
				MetricsDataId: int32(ps.config.Sub.DataId),
				AppName:       "pingserver",
			},
			Data: &define.PingserverData{
				DataId:  ps.config.Sub.DataId,
				Version: "v2",
				Data:    data,
			},
		})
	}
}
