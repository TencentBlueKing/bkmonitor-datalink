// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	// gseAgentInfoTimeout 是等待 Agent 回送主机信息的上限。上报载荷要带 Agent 侧的
	// 主机身份，拿不到就不发——缺少这些字段的数据在接收端无法归属到主机。
	gseAgentInfoTimeout = 10 * time.Second

	gseAgentInfoInterval = 500 * time.Millisecond

	gseAgentReadTimeout  = 5 * time.Second
	gseAgentWriteTimeout = 5 * time.Second

	gseAgentRetryTimes    = 3
	gseAgentRetryInterval = 3 * time.Second

	// maxGSEAgentInfoBytes 限制单次 GSE_TYPE_GET_CONF 响应体长度，防止异常报文触发大分配。
	maxGSEAgentInfoBytes = 1 << 20
)

var (
	newGSEConnection = func() gse.GseConnection {
		return gse.NewGseConnection()
	}
	gseAgentSleep = waitWithContext
)

// gseAgentSink 是轻量 CLI 的传输载体。CLI 由 JOB 拉起，运行在 Beat 框架之外，没有
// Beat 的事件通道与 gse output，因此自建单连接串行直写本机 GSE Agent。
//
// 不复用 libgse 的 GseClient.Start()：其后台会每 31 秒发送 GSE_TYPE_GET_CONF，与
// SendSync 无锁并发写同一个 Unix socket 会造成协议串流，且其 sendRawData 对写超时
// 会无限重试。CLI 是一次性短进程，启动时同步取一次 AgentInfo、发送时加锁串行并受
// ctx 与有限重试约束即可，退出即释放连接。
type gseAgentSink struct {
	mu       sync.Mutex
	endpoint string
	conn     gse.GseConnection
	info     gse.AgentInfo

	readTimeout   time.Duration
	writeTimeout  time.Duration
	retryTimes    int
	retryInterval time.Duration
}

// newGSEAgentSink 在采集开始之前就建立连接并取到 Agent 信息。连不上 Agent 时立刻
// 失败：此时 finished 与产物都发不出去，先采集 15~600 秒再报错只是浪费主机资源，
// 而 JOB 日志里能直接看到失败原因。
func newGSEAgentSink(ctx context.Context) (*gseAgentSink, error) {
	cfg, err := configs.ActiveProfileGSEConfigFromMain()
	if err != nil {
		return nil, err
	}
	return newGSEAgentSinkWithConfig(ctx, cfg)
}

func newGSEAgentSinkWithConfig(ctx context.Context, cfg configs.ActiveProfileGSEConfig) (*gseAgentSink, error) {
	sink := &gseAgentSink{
		endpoint:      cfg.Endpoint,
		readTimeout:   gseAgentReadTimeout,
		writeTimeout:  gseAgentWriteTimeout,
		retryTimes:    gseAgentRetryTimes,
		retryInterval: gseAgentRetryInterval,
	}
	if err := sink.connect(ctx); err != nil {
		return nil, fmt.Errorf("connect the local gse agent at %s: %w", cfg.Endpoint, err)
	}

	info, err := sink.waitAgentInfo(ctx, gseAgentInfoTimeout, gseAgentInfoInterval)
	if err != nil {
		sink.Close()
		return nil, err
	}
	info = applyGSEConfigOverrides(info, cfg)
	sink.info = info
	logger.Infof("[active_profile] gse agent endpoint=%s agent_info={%s}", cfg.Endpoint, info.String())
	return sink, nil
}

func (s *gseAgentSink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeConnLocked()
}

func (s *gseAgentSink) closeConnLocked() {
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
}

func (s *gseAgentSink) dialLocked() error {
	s.closeConnLocked()
	conn := newGSEConnection()
	conn.SetHost(s.endpoint)
	conn.SetTimeoutTime(s.readTimeout, s.writeTimeout)
	if err := conn.Dial(); err != nil {
		return err
	}
	s.conn = conn
	return nil
}

func (s *gseAgentSink) connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempts := s.retryTimes
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.dialLocked(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt+1 < attempts {
			if err := gseAgentSleep(ctx, s.retryInterval); err != nil {
				return err
			}
		}
	}
	return lastErr
}

// Send 写本机 Agent。补充的主机身份字段与常驻 Beat 的 gse output 一致：同一个
// DataID 的两个入口必须发出同形态的载荷。gseindex 由 output 从本地存储维护，CLI
// 不开存储，因此不携带。
func (s *gseAgentSink) Send(ctx context.Context, event define.Event) error {
	payload := event.AsMapStr()
	dataID, ok := payload["dataid"].(int32)
	if !ok {
		return fmt.Errorf("report payload carries no int32 dataid: %T", payload["dataid"])
	}
	payload["bizid"] = s.info.Bizid
	payload["bk_biz_id"] = s.info.BKBizID
	payload["cloudid"] = s.info.Cloudid
	payload["ip"] = s.info.IP
	payload["hostname"] = s.info.Hostname
	payload["bk_agent_id"] = s.info.BKAgentID
	payload["bk_host_id"] = s.info.HostID

	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal report payload for dataid %d: %w", dataID, err)
	}
	frame := gse.NewGseDynamicMsg(raw, dataID, 0, 0).ToBytes()

	if err := s.writeFrame(ctx, frame); err != nil {
		return fmt.Errorf("send %d bytes to the local gse agent for dataid %d: %w", len(raw), dataID, err)
	}
	logger.Infof("[active_profile] sent dataid=%d payload_bytes=%d to the local gse agent", dataID, len(raw))
	return nil
}

func (s *gseAgentSink) writeFrame(ctx context.Context, frame []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	attempts := s.retryTimes
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.conn == nil {
			if err := s.dialLocked(); err != nil {
				lastErr = err
				if attempt+1 < attempts {
					if sleepErr := gseAgentSleep(ctx, s.retryInterval); sleepErr != nil {
						return sleepErr
					}
				}
				continue
			}
		}
		writeTimeout, err := boundedTimeout(ctx, s.writeTimeout)
		if err != nil {
			return err
		}
		s.conn.SetTimeoutTime(s.readTimeout, writeTimeout)
		if err := writeFull(s.conn, frame); err == nil {
			return nil
		} else {
			lastErr = err
			// 写失败或超时可能已经写入部分字节，必须关闭当前连接避免后续消息错位串流。
			s.closeConnLocked()
		}
		if attempt+1 < attempts {
			if sleepErr := gseAgentSleep(ctx, s.retryInterval); sleepErr != nil {
				return sleepErr
			}
		}
	}
	return lastErr
}

// waitAgentInfo 同步向本机 Agent 请求主机信息并等待回送。
func (s *gseAgentSink) waitAgentInfo(
	ctx context.Context,
	timeout time.Duration,
	interval time.Duration,
) (gse.AgentInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return gse.AgentInfo{}, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return gse.AgentInfo{}, fmt.Errorf(
				"the local gse agent returned no host info within %s: the agent is running but not ready to accept data",
				timeout,
			)
		}
		info, err := s.fetchAgentInfoOnceLocked(ctx, remaining)
		if err == nil && !info.IsEmpty() {
			return info, nil
		}
		if err != nil {
			logger.Warnf("[active_profile] fetch gse agent info failed: %v", err)
			s.closeConnLocked()
		}
		remaining = time.Until(deadline)
		if remaining <= 0 {
			return gse.AgentInfo{}, fmt.Errorf(
				"the local gse agent returned no host info within %s: the agent is running but not ready to accept data",
				timeout,
			)
		}
		wait := interval
		if remaining < wait {
			wait = remaining
		}
		if err := gseAgentSleep(ctx, wait); err != nil {
			return gse.AgentInfo{}, err
		}
	}
}

func (s *gseAgentSink) fetchAgentInfoOnceLocked(ctx context.Context, remaining time.Duration) (gse.AgentInfo, error) {
	if s.conn == nil {
		if err := s.dialLocked(); err != nil {
			return gse.AgentInfo{}, err
		}
	}
	ioTimeout := remaining
	if s.readTimeout > 0 && s.readTimeout < ioTimeout {
		ioTimeout = s.readTimeout
	}
	ioTimeout, err := boundedTimeout(ctx, ioTimeout)
	if err != nil {
		return gse.AgentInfo{}, err
	}
	s.conn.SetTimeoutTime(ioTimeout, ioTimeout)

	if err := writeFull(s.conn, gse.NewGseRequestConfMsg().ToBytes()); err != nil {
		return gse.AgentInfo{}, err
	}

	var head [8]byte
	if _, err := io.ReadFull(s.conn, head[:]); err != nil {
		return gse.AgentInfo{}, err
	}
	msgType := binary.BigEndian.Uint32(head[:4])
	bodyLen := binary.BigEndian.Uint32(head[4:])
	if bodyLen > maxGSEAgentInfoBytes {
		return gse.AgentInfo{}, fmt.Errorf("gse agent response length %d exceeds limit %d", bodyLen, maxGSEAgentInfoBytes)
	}
	body := make([]byte, bodyLen)
	if bodyLen > 0 {
		if _, err := io.ReadFull(s.conn, body); err != nil {
			return gse.AgentInfo{}, err
		}
	}
	if msgType != gse.GSE_TYPE_GET_CONF {
		return gse.AgentInfo{}, fmt.Errorf("unexpected gse agent response type %d", msgType)
	}
	var info gse.AgentInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return gse.AgentInfo{}, fmt.Errorf("decode gse agent info: %w", err)
	}
	if hostname, err := os.Hostname(); err == nil {
		info.Hostname = hostname
	}
	return info, nil
}

// applyGSEConfigOverrides 按常驻 Beat AgentInfoFetcher 相同规则应用主配置 output.bkpipe
// 中的主机身份覆写，避免 CLI 顶层字段（特别是 bk_host_id / cloudid / dynamic addressing）
// 与常驻 Beat 产生分歧。
func applyGSEConfigOverrides(info gse.AgentInfo, cfg configs.ActiveProfileGSEConfig) gse.AgentInfo {
	if cfg.CloudID != nil {
		info.Cloudid = *cfg.CloudID
	}
	if cfg.HostIP != "" {
		info.IP = cfg.HostIP
	}
	if cfg.HostID != 0 {
		info.HostID = cfg.HostID
	}
	if cfg.BKAddressing == "dynamic" {
		info.IP = ""
	}
	return info
}

func boundedTimeout(ctx context.Context, fallback time.Duration) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, context.DeadlineExceeded
		}
		if fallback <= 0 || remaining < fallback {
			return remaining, nil
		}
	}
	return fallback, nil
}

func writeFull(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func waitWithContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
