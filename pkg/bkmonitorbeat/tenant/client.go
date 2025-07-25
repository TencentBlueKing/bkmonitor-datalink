// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tenant

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	agentmessage "github.com/TencentBlueKing/bk-gse-sdk/go/service/agent-message"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Option struct {
	Version string
	IPC     string
	Tasks   []string
}

type Client struct {
	ctx    context.Context
	cancel context.CancelFunc

	opt   Option
	agent agentmessage.Client
	pacer *Pacer
}

// innerLogger 实现 gseagent 定义 Logger 接口
type innerLogger struct{}

func (innerLogger) Debug(format string, args ...interface{}) {
	logger.Debugf(format, args...)
}

func (innerLogger) Info(format string, args ...interface{}) {
	logger.Infof(format, args...)
}

func (innerLogger) Warn(format string, args ...interface{}) {
	logger.Warnf(format, args...)
}

func (innerLogger) Error(format string, args ...interface{}) {
	logger.Errorf(format, args...)
}

func NewClient(opt Option) (*Client, error) {
	cli, err := agentmessage.New(
		agentmessage.WithPluginName("bkmonitorbeat"),
		agentmessage.WithPluginVersion(opt.Version),
		agentmessage.WithDomainSocketPath(opt.IPC),
		agentmessage.WithRecvCallback(func(msgID string, content []byte) {
			type R struct {
				Data []FetchHostDataIDData `json:"data"`
			}
			var rsp R
			if err := json.Unmarshal(content, &rsp); err != nil {
				logger.Errorf("failed to unmarshal agent.msg (%s): %v", msgID, err)
				return
			}
			logger.Debugf("handle agent.msg (%s)", msgID)

			tasks := make(map[string]int32)
			for _, pair := range rsp.Data {
				tasks[pair.Task] = pair.DataID
			}

			// 如果触发了更新 则需要通知采集器进行 reload
			updated := DefaultStorage().UpdateTaskDataIDs(tasks)
			if updated {
				beat.ReloadChan <- true
				define.RecordLog("update tenant dataid", []define.LogKV{{
					K: "tasks",
					V: tasks,
				}})
			}
		}),
		agentmessage.WithLogger(innerLogger{}),
	)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		ctx:    ctx,
		cancel: cancel,
		agent:  cli,
		opt:    opt,
		pacer:  newPacer(3600), // 最大间隔 1 小时
	}, nil
}

const (
	// Type 命名规则为 {动作}/{影响范围}/{操作对象}

	// TypeFetchHostDataID 获取平台内置的主机相关 dataid
	TypeFetchHostDataID = "fetch/host/dataid"
)

type FetchHostDataIDParams struct {
	Tasks []string `json:"tasks"`
}

type FetchHostDataIDData struct {
	Task   string `json:"task"`
	DataID int32  `json:"dataid"`
}

type AgentMsgRequest struct {
	Type     string      `json:"type"` // 后续可能会有其他的请求类型
	CloudID  int         `json:"cloudid"`
	AgentID  string      `json:"bk_agent_id"`
	TenantID string      `json:"bk_tenant_id"`
	IP       string      `json:"ip"`
	Params   interface{} `json:"params"`
}

func (c *Client) SendMsg(messageID string, content []byte) error {
	logger.Debugf("send agent.msg (%s), content=(%s)", messageID, content)
	return c.agent.SendMessage(c.ctx, messageID, content)
}

func (c *Client) Close() error {
	c.cancel()
	return c.agent.Terminate(c.ctx)
}

func (c *Client) Start() error {
	err := c.agent.Launch(c.ctx)
	if err != nil {
		return err
	}
	go c.loop()
	return nil
}

func (c *Client) loop() {
	send := func() {
		info, _ := gse.GetAgentInfo()
		// msgID 规则为 {插件名称}.{查询类型}.{UnixTimestamp}
		messageID := fmt.Sprintf("bkmonitorbeat.%s.%d", TypeFetchHostDataID, time.Now().Unix())
		content, _ := json.Marshal(AgentMsgRequest{
			Type:     TypeFetchHostDataID,
			CloudID:  int(info.Cloudid),
			AgentID:  info.BKAgentID,
			IP:       info.IP,
			TenantID: info.BKTenantID,
			Params: FetchHostDataIDParams{
				Tasks: c.opt.Tasks,
			},
		})
		if err := c.SendMsg(messageID, content); err != nil {
			logger.Errorf("failed to send (%s) msg: %v", TypeFetchHostDataID, err)
		}
	}

	wait := time.NewTimer(time.Duration(rand.Int()%60) * time.Second)
	select {
	case <-wait.C:
		send() // 启动即通信 但需要打散在 1min 内
	case <-c.ctx.Done():
		return
	}

	timer := time.NewTimer(time.Duration(c.pacer.Next()) * time.Second)
	for {
		select {
		case <-timer.C:
			send()
			timer.Reset(time.Duration(c.pacer.Next()) * time.Second)

		case <-c.ctx.Done():
			return
		}
	}
}

type Pacer struct {
	maxSeconds int
	count      int
}

func newPacer(maxSeconds int) *Pacer {
	return &Pacer{
		maxSeconds: maxSeconds,
	}
}

func (p *Pacer) Next() int {
	p.count++

	n := 1 << p.count
	seconds := (n * 60) + (rand.Int() % (n * 60))
	if seconds > p.maxSeconds {
		return p.maxSeconds
	}
	return seconds
}
