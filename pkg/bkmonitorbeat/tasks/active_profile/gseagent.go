// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"context"
	"encoding/json"
	"fmt"
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

	gseAgentWriteTimeout = 5 * time.Second

	gseAgentRetryTimes    = 3
	gseAgentRetryInterval = 3 * time.Second
)

// gseAgentSink 是轻量 CLI 的传输载体。CLI 由 JOB 拉起，运行在 Beat 框架之外，没有
// Beat 的事件通道与 gse output，因此自建 GSE 客户端直接写本机 Agent。
//
// 客户端只属于本次 CLI 进程：不抢 Beat 的 PID 锁、不打开本地存储，退出即释放。
type gseAgentSink struct {
	client *gse.GseClient
	info   gse.AgentInfo
}

// newGSEAgentSink 在采集开始之前就建立连接并取到 Agent 信息。连不上 Agent 时立刻
// 失败：此时 finished 与产物都发不出去，先采集 15~600 秒再报错只是浪费主机资源，
// 而 JOB 日志里能直接看到失败原因。
func newGSEAgentSink(ctx context.Context) (*gseAgentSink, error) {
	endpoint, err := configs.ActiveProfileGSEEndpoint()
	if err != nil {
		return nil, err
	}
	client, err := gse.NewGseClientFromConfig(gse.Config{
		RetryTimes:    gseAgentRetryTimes,
		RetryInterval: gseAgentRetryInterval,
		// 队列长度为 0：CLI 用 SendSync 直发，不经过异步队列，避免进程退出时把
		// 还没写出的终态丢在队列里。
		MsgQueueSize: 0,
		WriteTimeout: gseAgentWriteTimeout,
		Endpoint:     endpoint,
	})
	if err != nil {
		return nil, fmt.Errorf("create gse client for %s: %w", endpoint, err)
	}
	if err := client.Start(); err != nil {
		return nil, fmt.Errorf("connect the local gse agent at %s: %w", endpoint, err)
	}

	info, err := waitGSEAgentInfo(ctx, client)
	if err != nil {
		client.CloseSilent()
		return nil, err
	}
	logger.Infof("[active_profile] gse agent endpoint=%s agent_info={%s}", endpoint, info.String())
	return &gseAgentSink{client: client, info: info}, nil
}

func (s *gseAgentSink) Close() {
	s.client.CloseSilent()
}

// Send 写本机 Agent。补充的主机身份字段与常驻 Beat 的 gse output 一致：同一个
// DataID 的两个入口必须发出同形态的载荷。gseindex 由 output 从本地存储维护，CLI
// 不开存储，因此不携带。
func (s *gseAgentSink) Send(_ context.Context, event define.Event) error {
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
	if err := s.client.SendSync(gse.NewGseDynamicMsg(raw, dataID, 0, 0)); err != nil {
		return fmt.Errorf("send %d bytes to the local gse agent for dataid %d: %w", len(raw), dataID, err)
	}
	logger.Infof("[active_profile] sent dataid=%d payload_bytes=%d to the local gse agent", dataID, len(raw))
	return nil
}

// waitGSEAgentInfo 等待 Agent 回送主机信息。客户端在 Start 时请求一次，之后每 31
// 秒刷新，首次回送有延迟，因此这里轮询而不是只读一次。
func waitGSEAgentInfo(ctx context.Context, client *gse.GseClient) (gse.AgentInfo, error) {
	deadline := time.Now().Add(gseAgentInfoTimeout)
	for {
		info, err := client.GetAgentInfo()
		if err == nil && !info.IsEmpty() {
			return info, nil
		}
		if time.Now().After(deadline) {
			return gse.AgentInfo{}, fmt.Errorf(
				"the local gse agent returned no host info within %s: the agent is running but not ready to accept data",
				gseAgentInfoTimeout,
			)
		}
		select {
		case <-ctx.Done():
			return gse.AgentInfo{}, ctx.Err()
		case <-time.After(gseAgentInfoInterval):
		}
	}
}
