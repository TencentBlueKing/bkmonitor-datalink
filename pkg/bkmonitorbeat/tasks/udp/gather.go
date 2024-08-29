// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package udp

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// UDP 拨测状态码详情
//
// status: 拨测是否成功
// 1) Unknown = -1	-> 初始化状态
// 2) OK      = 0	-> 探测不超时 && 响应内容匹配合法
// 3) Error   = 1	-> !OK
//
// available: 单点可用率
// 1) Available 	= 1
// 2) Unavailable	= 0
//
// error_code: 业务层状态码 用于描述 status 具体失败原因
// DetectedSuccess		= 0	   -> 拨测成功
// CodeConnError		= 1000 -> 链接失败（host 或者端口非法）
// RequestError			= 2000 -> 请求写失败（syscall.write 返回错误）
// RequestInitError		= 2003 -> 请求初始化失败（"request"/"request_format" 解析出错）
// RequestDeadLineError = 2002 -> 超时设置错误
// ResponseError 		= 3000 -> 响应读取失败（其他情况）
// ResponseTimeoutError = 3001 -> 响应读取超时
// ResponseMatchError	= 3002 -> 响应内容匹配失败
// ResponseConnRefused  = 3007 -> 响应失败（ICMP unreachable）

type Gather struct {
	config *configs.UDPTaskConfig
	tasks.BaseTask
	bufferBuilder tasks.BufferBuilder
}

type Event struct {
	*tasks.SimpleEvent
	Times    int
	MaxTimes int
}

func (e *Event) AsMapStr() common.MapStr {
	mapStr := e.SimpleEvent.AsMapStr()
	mapStr["times"] = e.Times
	mapStr["max_times"] = e.MaxTimes
	return mapStr
}

func (e *Event) GetType() string {
	return define.ModuleUDP
}

func NewEvent(t *Gather, startAt time.Time, taskHost string) *Event {
	simpleEvent := tasks.NewSimpleEvent(t)
	simpleEvent.TargetHost = taskHost
	simpleEvent.TargetPort = t.config.TargetPort
	simpleEvent.StartAt = startAt
	return &Event{
		SimpleEvent: simpleEvent,
	}
}

func (g *Gather) checkTargetHost(ctx context.Context, targetHost string, event *Event) define.NamedCode {
	var (
		times int
		body  []byte
		code  define.NamedCode
	)
Loop:
	// 按配置的重试次数执行
	for times = 0; times < g.config.Times; times++ {
		// 连接
		conn, err := NewConn(ctx, g.config, targetHost)
		if err != nil {
			logger.Errorf("failed to build connection: %v", err)
			return define.CodeConnFailed
		}
		defer func() {
			err := conn.Close()
			if err != nil {
				logger.Warnf("%v: close conn error: %v", g.TaskConfig.GetTaskID(), err)
			}
		}()

		// 发送请求，获取响应内容
		event.StartAt = time.Now()
		body, code = g.detect(conn)
		if g.config.Response == "" {
			// 无响应匹配时无需计算耗时
			event.EndAt = event.StartAt
		} else {
			event.EndAt = time.Now()
		}
		switch code {
		case define.CodeOK, define.CodeConnRefused:
			// 成功以及对端不存在两种情况直接 break
			break Loop
		}
	}

	// 拨测次数更新
	event.Times = times

	// 拨测失败
	if code != define.CodeOK {
		return code
	}

	if g.config.Response != "" {
		// 拨测成功 但响应内容不符预期
		matched := utils.IsMatch(g.config.ResponseFormat, body, []byte(g.config.Response))
		if !matched {
			return define.CodeResponseNotMatch
		}
	}

	// 拨测成功 且响应内容符合预期
	return define.CodeOK
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	g.PreRun(ctx)
	defer g.PostRun(ctx)

	ctx, cancel := context.WithTimeout(ctx, g.config.Timeout)
	defer cancel()
	resultMap := make(map[string][]string)
	start := time.Now()

	hosts := g.config.Hosts()
	if len(hosts) == 0 {
		return
	}

	hostsInfo := tasks.GetHostsInfo(ctx, hosts, g.config.DNSCheckMode, g.config.TargetIPType, configs.Udp)
	for _, h := range hostsInfo {
		if h.Errno != define.CodeOK {
			event := NewEvent(g, start, h.Host)
			event.Fail(h.Errno)
			e <- event
			continue
		} else {
			resultMap[h.Host] = h.Ips
		}
	}

	// 解析目标为ip列表
	var wg sync.WaitGroup
	for taskHost, result := range resultMap {
		for _, targetHost := range result {
			// 获取并发限制信号量
			err := g.GetSemaphore().Acquire(ctx, 1)
			if err != nil {
				logger.Errorf("task(%d) semaphore acquire failed", g.TaskConfig.GetTaskID())
				return
			}

			wg.Add(1)
			go func(tHost, host string) {
				// 初始化事件
				event := NewEvent(g, start, tHost)
				event.ResolvedIP = host

				defer func() {
					wg.Done()
					g.GetSemaphore().Release(1)
					e <- event
				}()
				// 检查单个目标
				code := g.checkTargetHost(ctx, host, event)
				// 结束时间已在处理过程中配置，需保留
				end := event.EndAt
				if code == define.CodeOK {
					event.Success()
				} else {
					event.Fail(code)
				}
				if !end.IsZero() {
					event.EndAt = end
				}
			}(taskHost, targetHost)
		}
	}
	wg.Wait()
}

// NewConn 对于 UDP 而言并不会产生 `链接` 状态
// 这里返回错误只会是 address 不合法
var NewConn = func(ctx context.Context, config *configs.UDPTaskConfig, targetHost string) (net.Conn, error) {
	dialer := net.Dialer{
		Timeout: config.Timeout,
	}
	address := net.JoinHostPort(targetHost, strconv.Itoa(config.TargetPort))
	network := "udp"
	switch config.TargetIPType {
	case configs.IPv4:
		network = "udp4"
	case configs.IPv6:
		network = "udp6"
	}
	// 判断addr是否为有效的ipv4 ipv6 或者域名
	ret := utils.CheckIpOrDomainValid(targetHost)
	if ret == utils.Domain {
		network = "udp"
	}
	return dialer.DialContext(ctx, network, address)
}

// detect 发送请求到对端服务 并返回响应内容
func (g *Gather) detect(conn net.Conn) ([]byte, define.NamedCode) {
	msg, err := utils.ConvertStringToBytes(g.config.Request, g.config.RequestFormat)
	if err != nil {
		logger.Errorf("failed to convert strings to bytes: %v", err)
		return nil, define.CodeBadRequestParams
	}

	if _, err = conn.Write(msg); err != nil {
		logger.Errorf("failed to write udp message: %v", err)
		return nil, define.CodeRequestFailed
	}

	err = conn.SetDeadline(time.Now().Add(g.config.AvailableDuration))
	if err != nil {
		logger.Errorf("failed to set connection deadline: %v", err)
		return nil, define.CodeRequestFailed
	}

	body := make([]byte, g.config.BufferSize)
	n, err := conn.Read(body)
	if err != nil && err != io.EOF {
		if strings.Contains(err.Error(), "connection refused") {
			logger.Errorf("cause the connection refuse error: %v", err)
			return nil, define.CodeConnRefused
		}
		if g.config.Response != "" || (g.config.Response == "" && g.config.WaitEmptyResponse) {
			if strings.Contains(err.Error(), "timeout") {
				logger.Errorf("cause the timeout error: %v", err)
				return nil, define.CodeRequestTimeout
			}

			logger.Errorf("failed to read connection body: %v", err)
			return nil, define.CodeResponseFailed
		}
	}
	return body[:n], define.CodeOK
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.config = taskConfig.(*configs.UDPTaskConfig)

	logger.Infof("UDP task config: %v", gather.config)

	gather.Init()
	return gather
}
