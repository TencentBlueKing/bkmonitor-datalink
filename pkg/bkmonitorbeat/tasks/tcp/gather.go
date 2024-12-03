// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tcp

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func noNeedMatch(taskConf *configs.TCPTaskConfig) bool {
	return taskConf.Response == ""
}

// NewConn :
var NewConn = func(ctx context.Context, taskConf *configs.TCPTaskConfig, addr string) (net.Conn, error) {
	dialer := net.Dialer{
		Timeout: taskConf.Timeout,
	}

	return dialer.DialContext(ctx, "tcp", addr)
}

// Gather :
type Gather struct {
	tasks.BaseTask
	bufferBuilder tasks.BufferBuilder
}

func (g *Gather) newEvent(taskConf *configs.TCPTaskConfig, taskHost string) *tasks.SimpleEvent {
	event := tasks.NewSimpleEvent(g)
	event.StartAt = time.Now()
	event.TargetHost = taskHost
	event.TargetPort = taskConf.TargetPort
	return event
}

func (g *Gather) checkTargetHost(ctx context.Context, taskConf *configs.TCPTaskConfig, targetHost string, event *tasks.SimpleEvent) define.NamedCode {
	// 连接
	address := net.JoinHostPort(targetHost, strconv.Itoa(taskConf.TargetPort))
	conn, err := NewConn(ctx, taskConf, address) // for mock
	if err != nil {
		logger.Debugf("%v: connect %v fail: %v", taskConf.TaskID, address, err)
		if e, ok := err.(net.Error); ok && e.Timeout() {
			return define.CodeConnTimeout
		}
		return define.CodeConnFailed
	}

	defer func() {
		event.EndAt = time.Now() // tcp 三次握手后统计耗时 挥手不计入耗时内
		err := conn.Close()
		if err != nil {
			logger.Warnf("%v: close conn error: %v", taskConf.TaskID, err)
		}
	}()

	logger.Debugf("%v: connect %v success", taskConf.TaskID, address)
	// 无需检查情况直接返回成功
	if noNeedMatch(taskConf) {
		logger.Debugf("%v: return without match", taskConf.TaskID)
		return define.CodeOK
	}
	// 设置超时
	logger.Debugf("%v: set deadline: %v", taskConf.TaskID, taskConf.Timeout)
	err = conn.SetDeadline(event.StartAt.Add(taskConf.Timeout))
	if err != nil {
		logger.Warnf("%v: set deadline error: %v", taskConf.TaskID, err)
		return define.CodeRequestFailed
	}
	// 按配置发送请求
	if len(taskConf.Request) > 0 {
		requestData, err := utils.ConvertStringToBytes(taskConf.Request, taskConf.RequestFormat)
		if err != nil {
			logger.Warnf("%v: make request failed: %v", taskConf.TaskID, err)
			return define.CodeBadRequestParams
		}

		logger.Debugf("%v: request: %s", taskConf.TaskID, requestData)
		count, err := conn.Write(requestData)
		if err != nil {
			logger.Debugf("%v: write request error: %v", taskConf.TaskID, err)
			if e, ok := err.(net.Error); ok && e.Timeout() {
				return define.CodeRequestTimeout
			}
			logger.Debugf("%v: write error: %v", taskConf.TaskID, err)
			return define.CodeRequestFailed
		}
		logger.Debugf("%v: written data length: %v", taskConf.TaskID, count)
	}
	// 检查响应
	if len(taskConf.Response) > 0 {
		// 读取响应内容
		response := g.bufferBuilder.GetBuffer(taskConf.BufferSize)
		count, err := conn.Read(response)
		if err != nil && err != io.EOF {
			logger.Debugf("%v: read error: %v", taskConf.TaskID, err)
			if e, ok := err.(net.Error); ok && e.Timeout() {
				return define.CodeRequestTimeout
			}
			logger.Debugf("%v: read error: %v", taskConf.TaskID, err)
			return define.CodeResponseFailed
		}
		response = response[:count]
		logger.Debugf("%v: response: %s", taskConf.TaskID, response)

		logger.Debugf("%v: read response with length %v: %s", taskConf.TaskID, count, response)
		// 判断响应内容是否匹配配置
		ok := utils.IsMatch(taskConf.ResponseFormat, response, []byte(taskConf.Response))
		if !ok {
			logger.Debugf("%v: match response fail with type[%v]", taskConf.TaskID, taskConf.ResponseFormat)
			return define.CodeResponseNotMatch
		}
	}
	return define.CodeOK
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	resultMap := make(map[string][]string)
	taskConf := g.TaskConfig.(*configs.TCPTaskConfig)
	g.PreRun(ctx)
	defer g.PostRun(ctx)

	hosts := taskConf.Hosts()
	if len(hosts) == 0 {
		return
	}

	hostsInfo := tasks.GetHostsInfo(ctx, hosts, taskConf.DNSCheckMode, taskConf.TargetIPType, configs.Tcp)
	for _, h := range hostsInfo {
		if h.Errno != define.CodeOK {
			event := g.newEvent(taskConf, h.Host)
			event.Fail(h.Errno)
			// 如果需要使用自定义上报，则将事件转换为自定义事件
			if taskConf.CustomReport {
				e <- tasks.NewCustomEventBySimpleEvent(event)
			} else {
				e <- event
			}
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
				event := g.newEvent(taskConf, tHost)
				event.ResolvedIP = host

				defer func() {
					wg.Done()
					g.GetSemaphore().Release(1)

					// 如果需要使用自定义上报，则将事件转换为自定义事件
					if taskConf.CustomReport {
						e <- tasks.NewCustomEventBySimpleEvent(event)
					} else {
						e <- event
					}
				}()
				// 检查单个目标
				code := g.checkTargetHost(ctx, taskConf, host, event)
				if code == define.CodeOK {
					event.SuccessOrTimeout()
				} else {
					event.Fail(code)
				}
			}(taskHost, targetHost)
		}
	}
	wg.Wait()
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.Init()

	return gather
}
