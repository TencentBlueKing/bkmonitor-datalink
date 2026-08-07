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
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
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

	sequenceMu  sync.Mutex
	instanceID  string
	maxIssued   uint64
	maxAccepted uint64
	storage     *Storage
	onUpdate    func(map[string]int32)
	retrySoon   chan struct{}
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
	instanceID, err := newInstanceID()
	if err != nil {
		return nil, fmt.Errorf("generate tenant client instance ID: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{
		ctx:        ctx,
		cancel:     cancel,
		opt:        opt,
		pacer:      newPacer(3600), // 最大间隔 1 小时
		instanceID: instanceID,
		storage:    DefaultStorage(),
		retrySoon:  make(chan struct{}, 1),
	}
	client.onUpdate = func(tasks map[string]int32) {
		beat.ReloadChan <- true
		define.RecordLog("update tenant dataid", []define.LogKV{{
			K: "tasks",
			V: tasks,
		}})
	}

	socketOpts, err := agentMessageSocketOptions(opt.IPC)
	if err != nil {
		cancel()
		return nil, err
	}

	opts := []agentmessage.OptionFn{
		agentmessage.WithPluginName("bkmonitorbeat"),
		agentmessage.WithPluginVersion(opt.Version),
	}
	opts = append(opts, socketOpts...)
	opts = append(opts,
		agentmessage.WithRecvCallback(client.handleMessage),
		agentmessage.WithLogger(innerLogger{}),
	)
	cli, err := agentmessage.New(opts...)
	if err != nil {
		cancel()
		return nil, err
	}

	client.agent = cli
	return client, nil
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

func newInstanceID() (string, error) {
	buf := make([]byte, 8)
	if _, err := cryptorand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (c *Client) nextMessageID() string {
	c.sequenceMu.Lock()
	defer c.sequenceMu.Unlock()

	c.maxIssued++
	// Metadata treats message IDs as opaque values and echoes them in responses.
	return fmt.Sprintf(
		"bkmonitorbeat.%s.%s.%s",
		TypeFetchHostDataID,
		c.instanceID,
		strconv.FormatUint(c.maxIssued, 36),
	)
}

func (c *Client) responseSequence(messageID string) (uint64, bool) {
	prefix := fmt.Sprintf("bkmonitorbeat.%s.%s.", TypeFetchHostDataID, c.instanceID)
	if !strings.HasPrefix(messageID, prefix) {
		return 0, false
	}
	sequenceText := strings.TrimPrefix(messageID, prefix)
	if sequenceText == "" || strings.Contains(sequenceText, ".") {
		return 0, false
	}
	sequence, err := strconv.ParseUint(sequenceText, 36, 64)
	if err != nil || sequence == 0 {
		return 0, false
	}
	return sequence, true
}

func (c *Client) handleMessage(messageID string, content []byte) {
	type response struct {
		Code int                   `json:"code"`
		Data []FetchHostDataIDData `json:"data"`
	}
	var rsp response
	if err := json.Unmarshal(content, &rsp); err != nil {
		logger.Errorf("failed to unmarshal agent.msg (%s): %v", messageID, err)
		return
	}
	if rsp.Code != 0 {
		logger.Errorf("failed tenant dataid response (%s), code=%d", messageID, rsp.Code)
		return
	}
	sequence, ok := c.responseSequence(messageID)
	if !ok {
		logger.Warnf("ignore tenant dataid response with unknown message ID (%s)", messageID)
		return
	}

	tasks := make(map[string]int32, len(rsp.Data))
	for _, pair := range rsp.Data {
		tasks[pair.Task] = pair.DataID
	}

	c.sequenceMu.Lock()
	if sequence > c.maxIssued || sequence < c.maxAccepted {
		c.sequenceMu.Unlock()
		logger.Warnf("ignore stale tenant dataid response (%s)", messageID)
		return
	}
	if sequence > c.maxAccepted {
		c.maxAccepted = sequence
	}
	updated := c.storage.UpdateTaskDataIDs(tasks)
	c.sequenceMu.Unlock()
	if c.storage.NeedsRefresh() {
		logger.Warnf("tenant dataid response remains incomplete or unapplied (%s)", messageID)
		c.retryMissingDataIDs()
	}

	logger.Debugf("handle agent.msg (%s)", messageID)
	if updated {
		c.onUpdate(tasks)
	}
}

func (c *Client) retryMissingDataIDs() {
	if c.pacer == nil || c.retrySoon == nil {
		return
	}
	c.pacer.Reset()
	select {
	case c.retrySoon <- struct{}{}:
	default:
	}
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
		messageID := c.nextMessageID()
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
			if c.storage.NeedsRefresh() {
				c.pacer.Reset()
			}
			send()
			timer.Reset(time.Duration(c.pacer.Next()) * time.Second)

		case <-c.retrySoon:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			c.pacer.Reset()
			timer.Reset(time.Duration(c.pacer.Next()) * time.Second)

		case <-c.ctx.Done():
			return
		}
	}
}

type Pacer struct {
	mut            sync.Mutex
	maxSeconds     int
	nextMinSeconds int
}

func newPacer(maxSeconds int) *Pacer {
	return &Pacer{
		maxSeconds:     maxSeconds,
		nextMinSeconds: 2 * 60,
	}
}

func (p *Pacer) Next() int {
	p.mut.Lock()
	defer p.mut.Unlock()

	if p.maxSeconds <= 0 {
		return 0
	}
	if p.nextMinSeconds >= p.maxSeconds {
		return p.maxSeconds
	}

	minSeconds := p.nextMinSeconds
	if minSeconds > p.maxSeconds/2 {
		p.nextMinSeconds = p.maxSeconds
	} else {
		p.nextMinSeconds = minSeconds * 2
	}

	seconds := minSeconds + rand.Intn(minSeconds)
	if seconds > p.maxSeconds {
		return p.maxSeconds
	}
	return seconds
}

func (p *Pacer) Reset() {
	p.mut.Lock()
	defer p.mut.Unlock()

	p.nextMinSeconds = 2 * 60
}
