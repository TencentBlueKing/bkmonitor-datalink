// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pprofprofile "github.com/google/pprof/profile"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

// newOverlapTestProfile 生成一份最小可解析的 CPU pprof，供假 endpoint 返回。
func newOverlapTestProfile(t *testing.T) []byte {
	t.Helper()
	profile := &pprofprofile.Profile{
		SampleType: []*pprofprofile.ValueType{{Type: "samples", Unit: "count"}},
		PeriodType: &pprofprofile.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Period:     int64(10 * time.Millisecond),
		Sample:     []*pprofprofile.Sample{{Value: []int64{1}}},
	}
	var buffer bytes.Buffer
	if err := profile.WriteUncompressed(&buffer); err != nil {
		t.Fatalf("write overlap test profile: %v", err)
	}
	return buffer.Bytes()
}

// TestGatherRejectsOverlappingRound 覆盖同任务重叠触发：调度器会在上一轮结束前启动
// 下一轮，两轮共用同一个 Gather 实例。重叠轮次必须不采集、不排队，并上报一条带忙碌
// 原因的 failed finished。
//
// 需要说明这个用例覆盖不到什么：它只验证上述行为契约。Gather.running 真正要消除的是
// BaseTask.PreRun/PostRun 对共享 Status 与并发限制字段的无锁修改，而那属于数据竞争，
// 本用例观察不到——为了制造确定性的重叠窗口必须引入通道同步，那条 happens-before 边
// 本身就会让 race detector 认为两次 PreRun 有序。
//
// 用无协调的并发多轮触发确实能让 detector 报出该竞争（已验证：摘掉守卫后必报），但同一
// 场景还会同时触发 tasks.DefaultIPs 中一处既有的 double-checked locking 竞争
// （default_ip.go:155 在 sync.Once 之外读 globalIPv4s），那属于共享代码的独立问题，
// 因此这里不把它做成常驻用例。
func TestGatherRejectsOverlappingRound(t *testing.T) {
	payload := newOverlapTestProfile(t)

	var fetches atomic.Int32
	var enteredOnce sync.Once
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		// 第一次请求进入后停住，制造确定性的重叠窗口，不依赖 sleep 时长。
		enteredOnce.Do(func() { close(entered) })
		<-release
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	conf := newTestGoTaskConfig(t, configs.ActiveProfileTypeGoCPU)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(endpoint.Port())
	if err != nil {
		t.Fatal(err)
	}
	conf.CollectorConfig.IPAddress = endpoint.Hostname()
	conf.CollectorConfig.Port = port
	conf.CollectorConfig.DurationSec = 1
	// 关掉资源保护：本例测的是重叠拦截。资源探针只在 Linux 上可用，开启后会在
	// 非 Linux 上 fail-closed，第一轮还没发出请求就中止。
	conf.ResourceGuardConfig = configs.ActiveProfileResourceGuardConfig{}
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}

	gather := newTestGather(conf)
	// Event、产物与终态指标都写入该通道，留足缓冲避免 finish 阻塞。
	events := newReportChannel()
	ctx := context.Background()

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		gather.Run(ctx, events)
	}()

	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		close(release)
		t.Fatal("the first round never reached the pprof endpoint")
	}

	// 第一轮仍停在采集中，此时触发第二轮。
	gather.Run(ctx, events)

	close(release)
	select {
	case <-firstDone:
	case <-time.After(30 * time.Second):
		t.Fatal("the first round did not finish")
	}

	if got := fetches.Load(); got != 1 {
		t.Errorf("pprof endpoint was fetched %d times, want exactly 1", got)
	}

	busyRounds := 0
	for _, event := range captureReports(t, events).Events {
		if strings.Contains(event.Event.Content, errTaskAlreadyRunning) {
			busyRounds++
		}
	}
	if busyRounds != 1 {
		t.Errorf("busy finished events = %d, want exactly 1 for the overlapping round", busyRounds)
	}
}
