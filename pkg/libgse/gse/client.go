// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package gse

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/logp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/monitoring"
)

// Config GseClient config
type Config struct {
	ReconnectTimes uint          `config:"reconnecttimes"`
	RetryTimes     uint          `config:"retrytimes"`
	RetryInterval  time.Duration `config:"retryinterval"`
	MsgQueueSize   uint          `config:"mqsize"`
	WriteTimeout   time.Duration `config:"writetimeout"`
	ReadTimeout    time.Duration `config:"readtimeout"`
	Endpoint       string        `config:"endpoint"`
	Nonblock       bool          `config:"nonblock"` // TODO not used now
}

const (
	EINVAL int = iota
	ErrNetClosing
	ErrIOTimeout
	EPIPE
	ErrNotConnected
)

var defaultConfig = Config{
	MsgQueueSize:   1,
	WriteTimeout:   5 * time.Second,
	ReadTimeout:    60 * time.Second,
	Nonblock:       false,
	RetryTimes:     3,
	RetryInterval:  3 * time.Second,
	ReconnectTimes: 3,
}

var (
	metricGseClientConnected     = monitoring.NewInt("gse_client_connected")      // 连接次数
	metricGseClientConnectRetry  = monitoring.NewInt("gse_client_connect_retry")  // 连接重试次数
	metricGseClientConnectFailed = monitoring.NewInt("gse_client_connect_failed") // 连接失败次数

	metricGseClientReceived    = monitoring.NewInt("gse_client_received")     // 接收的请求数
	metricGseClientClose       = monitoring.NewInt("gse_client_close")        // 客户端连接断开计数
	metricGseClientServerClose = monitoring.NewInt("gse_client_server_close") // 服务端连接断开计数
	metricGseClientSendTimeout = monitoring.NewInt("gse_client_send_timeout") // 采集器发送超时次数
	metricGseClientSendRetry   = monitoring.NewInt("gse_client_send_retry")   // 重试的请求数
	metricGseClientSendFailed  = monitoring.NewInt("gse_client_send_failed")  // 发送失败的数量
	metricGseClientSendTotal   = monitoring.NewInt("gse_client_send_total")   // 发送成功的数量

	metricGseAgentReceived      = monitoring.NewInt("gse_agent_received")
	metricGseAgentReceiveFailed = monitoring.NewInt("gse_agent_receive_failed")
	GseCheck                    = false
	IsContainerMode             = false
)

// GseClient : gse client
// used for send data and get agent info
type GseClient struct {
	socket       GseConnection
	agentInfo    AgentInfo
	quitChan     chan bool
	checkRetry   chan struct{}
	connectTimes uint        // 用于重连计数：达到限额后，将使用原socket进行通讯
	msgChan      chan GseMsg // msg queue
	msgQueueSize uint        // msg queue szie
	cfg          Config
	silentQuit   bool
}

// NewGseClient create a gse client
// host set to default gse ipc path, different from linux and windows
func NewGseClient(cfg *common.Config) (*GseClient, error) {
	// parse config
	c := defaultConfig
	err := cfg.Unpack(&c)
	if err != nil {
		logp.Err("unpack config error, %v", err)
		return nil, err
	}
	logp.Info("gse client config: %+v", c)

	cli := GseClient{
		cfg:          c,
		connectTimes: 0,
		msgQueueSize: c.MsgQueueSize,
	}
	cli.socket = NewGseConnection()
	cli.socket.SetTimeoutTime(c.ReadTimeout, c.WriteTimeout)
	if c.Endpoint != "" {
		cli.socket.SetHost(c.Endpoint)
	}
	cli.checkRetry = make(chan struct{}, 1)
	return &cli, nil
}

func NewGseClientFromConfig(c Config) (*GseClient, error) {
	logp.Info("gse client config: %+v", c)

	cli := GseClient{
		cfg:          c,
		msgQueueSize: c.MsgQueueSize,
	}
	cli.socket = NewGseConnection()
	cli.socket.SetTimeoutTime(c.ReadTimeout, c.WriteTimeout)
	if c.Endpoint != "" {
		cli.socket.SetHost(c.Endpoint)
	}
	cli.checkRetry = make(chan struct{}, 1)
	return &cli, nil
}

// Start : start client
// start to recv msg and get agent info
// run as goroutine
func (c *GseClient) Start() error {
	c.msgChan = make(chan GseMsg, c.msgQueueSize)
	c.quitChan = make(chan bool)

	err := c.connect()
	if err != nil {
		return err
	}

	go c.recvMsgFromAgent()
	// default request agent info evry 31s
	go c.updateAgentInfo(time.Second * 31)
	go c.msgSender()
	logp.Info("gse client start")
	return nil
}

// StartWithoutCheckConn : start client without check connection
func (c *GseClient) StartWithoutCheckConn() {
	c.msgChan = make(chan GseMsg, c.msgQueueSize)
	c.quitChan = make(chan bool)

	go c.recvMsgFromAgent()
	// default request agent info evry 31s
	go c.updateAgentInfo(time.Second * 31)
	go c.msgSender()
	logp.Info("gse client start no check")
}

// Close : release resources
func (c *GseClient) Close() {
	logp.Err("gse client closed")
	close(c.quitChan)
	c.socket.Close()
	return
}

// CloseSilent : release resources silently
// 静默关闭 不打印 Err 日志
func (c *GseClient) CloseSilent() {
	c.silentQuit = true
	close(c.quitChan)
	c.socket.Close()
	return
}

// ==========================================

// GetAgentInfo : get agent info
// client will update info from gse agent every 1min
// request from agent first time when client start
func (c *GseClient) GetAgentInfo() (AgentInfo, error) {
	return c.agentInfo, nil
}

// Send : send msg to client
// will bolck when queue is full
func (c *GseClient) Send(msg GseMsg) error {
	c.msgChan <- msg
	metricGseClientReceived.Add(1)
	return nil
}

// SendSync : send msg to client synchronously
func (c *GseClient) SendSync(msg GseMsg) error {
	metricGseClientReceived.Add(1)
	err := c.sendRawData(msg.ToBytes())
	if err != nil {
		metricGseClientSendFailed.Add(1)
		return err
	}
	metricGseClientSendTotal.Add(1)
	return nil
}

// SendWithNewConnection : send msg to client with new connection every time
func (c *GseClient) SendWithNewConnection(msg GseMsg) error {
	// new connection
	socket := NewGseConnection()
	err := socket.Dial()
	if err != nil {
		return err
	}
	defer socket.Close()

	retry := 3
	var n int
	for retry > 0 {
		n, err = socket.Write(msg.ToBytes())
		if err == nil {
			logp.Debug("gse", "send size: %d", n)
			break
		} else {
			logp.Err("gse client sendRawData failed, %v", err)
			c.reconnect()
			time.Sleep(1)
			retry--
		}
	}

	logp.Debug("gse", "send with new conneciton")
	return nil
}

// connect : connect to agent
// try to connect again several times until connected
// program will quit if failed finaly
func (c *GseClient) connect() error {
	retry := c.cfg.RetryTimes
	var err error
	retryTimes := 1

	// 判断连接时间间隔，默认 30s
	var intervalTime time.Duration
	if c.cfg.RetryInterval == 0 {
		intervalTime = time.Second * 30
	} else {
		intervalTime = c.cfg.RetryInterval
	}

	// 判断连接次数，默认为 1
	if retry == 0 {
		retry = 1
	}

	t0 := time.Now()
	// 连接 gse 的前置校验
	for GseCheck {
		// 检查 socket 是否能连接
		if err := c.socket.Dial(); err != nil {
			logp.Err("try connect gse socket %d times, err: %v", retryTimes, err)
			time.Sleep(intervalTime)
			retryTimes++
			continue
		}

		// 检查 socket 能否写数据 agentinfo 接口
		msg := NewGseRequestConfMsg()
		if err := c.sendRawData(msg.ToBytes()); err != nil {
			logp.Err("try send data to socket %d times, err: %v", retryTimes, err)
			time.Sleep(intervalTime)
			retryTimes++

			// 写失败 关闭链接 下次循环重新建链
			_ = c.socket.Close()
			continue
		}

		// 等待数据读取
		go c.recvMsgFromAgent()

		n := 0
		var recv bool

		// 持续 10s 钟未收到数据则判定异常
		for n <= 10 {
			n++
			if c.agentInfo.IsEmpty() {
				time.Sleep(time.Second)
			} else {
				recv = true
				break
			}
		}

		if !recv {
			// 未收到数据通知 recv goroutine 退出
			select {
			case c.checkRetry <- struct{}{}:
			default:
			}
			logp.Err("try recv data from socket %d times, agentInfo: %+v", retryTimes, c.agentInfo)
			time.Sleep(intervalTime)
			retryTimes++

			// 读失败 关闭链接 下次循环重新建链
			_ = c.socket.Close()
			continue
		}

		fmt.Printf("%s [Bingo]: gse check success, take: %v, program exit, agentInfo: %+v\n", time.Now().Format(time.RFC3339), time.Since(t0), c.agentInfo)
		c.CloseSilent()
		os.Exit(0) // 链接成功 退出检查
	}

	for retry > 0 {
		err = c.socket.Dial()
		if err == nil {
			metricGseClientConnected.Add(1)
			logp.Info("gse client socket connected")
			return nil
		}

		// 针对不同 error 需要单独处理
		// 如果 socket 文件不存在 那就不用尝试了（容器模式下）直接退出
		if strings.Contains(err.Error(), "no such file or directory") && IsContainerMode {
			logp.Err("no socket found, exit program")
			time.Sleep(3 * time.Second) // 避免频繁重启
			os.Exit(1)
		}

		metricGseClientConnectRetry.Add(1)
		logp.Err("try %d times", c.cfg.RetryTimes-retry)
		time.Sleep(intervalTime)
		retry--
	}
	metricGseClientConnectFailed.Add(1)
	return err
}

// reconnect: reconnect to agent
func (c *GseClient) reconnect() {
	logp.Err("gse client reconnecting...")

	// close quitChan will stop updateAgentInfo and msgSender goroutine
	// close(c.quitChan)
	c.socket.Close()

	err := c.connect()
	if err != nil {
		logp.Err("connect failed, %v", err)
		return
	}
}

// request agent info every interval time
func (c *GseClient) updateAgentInfo(interval time.Duration) {
	logp.Info("gse client start update agent info")
	err := c.requestAgentInfo()
	if err != nil {
		logp.Err("gse client send sync cfg command failed, %v", err)
	}
	for {
		select {
		case <-time.After(interval):
			logp.Debug("gse", "send sync cfg command")
			err := c.requestAgentInfo()
			if err != nil {
				logp.Err("gse client send sync cfg command failed, error %v", err)
				continue
			}
		case <-c.quitChan:
			if !c.silentQuit {
				logp.Err("gse client updateAgentInfo quit")
			}
			return
		}
	}
}

// msgSender : get msg from queue, send it to agent
func (c *GseClient) msgSender() {
	logp.Info("gse client start send msg")
	for {
		select {
		case msg := <-c.msgChan:
			err := c.sendRawData(msg.ToBytes())
			if err != nil {
				metricGseClientSendFailed.Add(1)
				// program quit if send error
				logp.Err("gse client send failed")
				continue
			}
			metricGseClientSendTotal.Add(1)
		case <-c.quitChan:
			if !c.silentQuit {
				logp.Err("gse client msgSender quit")
			}
			return
		}
	}
}

// sendRawData : send binary data
func (c *GseClient) sendRawData(data []byte) error {
	retry := c.cfg.RetryTimes
	var err error
	var n int
	for retry > 0 {
		n, err = c.socket.Write(data)
		if err == nil {
			logp.Debug("gse", "send size: %d", n)
			c.onWriteSuccess()
			break
		}

		// 发送重试: 根据连接状态及全局连接次数判断是否需要重连
		metricGseClientSendRetry.Add(1)
		opErrno := c.getOpErrno(err)
		isReconnect := c.isReconnectable(opErrno)
		if isReconnect {
			c.reconnect()
			c.onReconnectSuccess()
		}
		logp.Err(
			"gse client sendRawDat failed: isReconnect=>%t, connectTimes=>%d, Err=>%v",
			isReconnect, c.connectTimes, err)
		time.Sleep(c.cfg.RetryInterval)

		// 如果写超时则持续写入，避免数据丢失
		if opErrno == ErrIOTimeout {
			continue
		}
		retry--
	}
	return err
}

// 获取unix连接异常信息
func (c *GseClient) getOpErrno(err error) int {
	if err == syscall.EINVAL {
		return EINVAL
	}
	if err == errNoConnection {
		return ErrNotConnected
	}
	// 转换成*net.OpError
	opErr := (*net.OpError)(unsafe.Pointer(reflect.ValueOf(err).Pointer()))
	opError := opErr.Err.Error()
	if strings.Contains(opError, "i/o timeout") {
		return ErrIOTimeout
	} else if strings.Contains(opError, "pipe") {
		return EPIPE
	} else if strings.Contains(opError, "use of closed network connection") {
		return ErrNetClosing
	}
	return EINVAL
}

// isReconnectable： 用于写失败的重连判断
func (c *GseClient) isReconnectable(opErrno int) bool {
	// 写超时使用原连接进行重试
	if opErrno == ErrIOTimeout {
		metricGseClientSendTimeout.Add(1)
		return false
	}
	// 连接关闭后，直接进行重连
	if opErrno == ErrNetClosing || opErrno == ErrNotConnected {
		metricGseClientClose.Add(1)
		return true
	}

	// 当连接次数超过重连次数限制，使用原socket进行通讯
	return c.cfg.ReconnectTimes >= c.connectTimes
}

// 如果是服务端关闭，则重设连接次数
func (c *GseClient) onServerClose() {
	c.connectTimes = 0
	return
}

// 向gse agent写入成功时减少重连次数
func (c *GseClient) onReconnectSuccess() {
	c.connectTimes++
	return
}

// 向gse agent写入失败的处理操作
func (c *GseClient) onWriteSuccess() {
	if c.connectTimes > 0 {
		c.connectTimes--
	}
	return
}

// RequestAgentInfo : request agent info
func (c *GseClient) requestAgentInfo() error {
	logp.Debug("gse", "request agent info")
	msg := NewGseRequestConfMsg()
	return c.Send(msg)
}

// agentInfoMsgHandler: parse to agent info
func (c *GseClient) agentInfoMsgHandler(buf []byte) {
	var err error
	if err = json.Unmarshal(buf, &c.agentInfo); nil != err {
		logp.Err("gse client data is not json, %s", string(buf))
	}
	c.agentInfo.Hostname, err = os.Hostname()
	if err != nil {
		c.agentInfo.Hostname = ""
	}
	logp.Debug("gse", "update agent info, %+v", c.agentInfo)
}

func (c *GseClient) recvMsgFromAgent() {
	logp.Info("gse client start recv msg")
	for {
		select {
		case <-c.checkRetry:
			return
		case <-c.quitChan:
			if !c.silentQuit {
				logp.Err("gse client msgSender quit")
			}
			return
		default:
			// read head
			headbufLen := 8 // GseLocalCommandMsg size
			headbuf := make([]byte, headbufLen)
			len, err := c.socket.Read(headbuf)

			// err handle
			if err == io.EOF {
				// socket closed by agent
				if !c.silentQuit {
					logp.Err("socket closed by remote")
				}
				metricGseClientServerClose.Add(1)
				c.reconnect()
				c.onServerClose()
				continue
			} else if err != nil {
				metricGseAgentReceiveFailed.Add(1)
				if !c.silentQuit {
					logp.Err("gse client recv err %v", err)
				}
				time.Sleep(time.Second)
				continue
			} else if len != headbufLen {
				metricGseAgentReceiveFailed.Add(1)
				if !c.silentQuit {
					logp.Err("gse client recv only %d bytes", len)
				}
				continue
			}
			metricGseAgentReceived.Add(1)

			logp.Debug("gse", "recv len : %d", len)
			// logp.Debug("gse", "headbuf : %s", headbuf)

			// get type and data len
			var msg GseLocalCommandMsg
			msg.MsgType = binary.BigEndian.Uint32(headbuf[:4])
			msg.BodyLen = binary.BigEndian.Uint32(headbuf[4:])
			logp.Debug("gse", "msg type=%d, len=%d", msg.MsgType, msg.BodyLen)

			// TODO now only has GSE_TYPE_GET_CONF type
			if msg.MsgType == GSE_TYPE_GET_CONF {
				// read data
				databuf := make([]byte, msg.BodyLen)
				if _, err := c.socket.Read(databuf); nil != err && err != io.EOF {
					logp.Err("gse client read err, %v", err)
					continue
				}
				c.agentInfoMsgHandler(databuf)
			} else {
				// get other data
			}
		}
	}
	logp.Err("gse client recvMsgFromAgent quit")
}
