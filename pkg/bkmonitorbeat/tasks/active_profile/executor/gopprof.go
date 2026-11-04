// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package executor

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	pprofprofile "github.com/google/pprof/profile"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	goPprofCPUPath  = "/debug/pprof/profile"
	goPprofHeapPath = "/debug/pprof/heap"

	// goPprofResponseLimit 限制单次响应体积。Go 的 pprof 产物远小于该上限，设界
	// 只为防止异常 endpoint 返回无界响应体。
	goPprofResponseLimit = 64 << 20

	// goPprofErrorDetailLimit 限制非 200 响应体读取量。pprof handler 以 text/plain
	// 返回拒绝原因，取开头一段附进错误信息即可定位。
	goPprofErrorDetailLimit = 4 << 10

	// goPprofHeapTimeout 是 heap 快照的请求预算。heap 没有采样窗口，协议中也没有
	// duration_sec，因此不按时长推导。
	goPprofHeapTimeout = 30 * time.Second

	// goPprofFetchSlack 是 CPU 采样窗口之外留给建连、传输和目标进程收尾的余量。
	goPprofFetchSlack = 30 * time.Second

	// goPprofParseBaseBytes 与 goPprofParseFactor 估算解析、加标签和重新序列化一份 pprof 的
	// 内存：实测 Beat 增量为未压缩大小的 12~19 倍，外加十几 MB 的固定开销。
	goPprofParseBaseBytes = 20 * bytesPerMB
	goPprofParseFactor    = 20
)

// goPprofClient 不使用 http.DefaultClient：采集目标是主机上的 pprof endpoint，
// 走环境变量里的代理只会把本机请求发到无关出口。空 Transport 的 Proxy 为 nil，
// 即直连。超时由每次请求的 context 控制，因此不设 Client.Timeout。
var goPprofClient = &http.Client{Transport: &http.Transport{}}

// RunGoPprof 从目标进程的 net/http/pprof endpoint 取一次 pprof 产物。响应本身已经
// 是带符号的 pprof，因此这里不涉及 perf script 转换与外部符号化，只补齐执行定位标签。
//
// 产物全程留在内存里，正常路径不落盘：endpoint 的响应读完时整份数据已经在内存中，
// 解析后的对象图比它还大，写盘再读回来上传不降低内存峰值，只多一次写读。
//
// 资源保护与 perf 路径用同一套策略，不做分叉：min_mem_level 判的是宿主可用内存，与采集
// 方式无关，宿主吃紧就该停止采集；max_disk_usage 量的是本次运行目录的占用，这条路径正常
// 不写文件，因此配额恒不触发，留着即可，不必特例关闭。
func RunGoPprof(
	ctx context.Context,
	conf *configs.ActiveProfileTaskConfig,
	req Request,
) (result *Result, returnErr error) {
	result = &Result{}
	endpoint, timeout, err := goPprofEndpoint(conf)
	if err != nil {
		return result, err
	}

	// 取消原因要在 fetch 读到 context 错误之前发布出来，否则熔断会退化成一条无信息的
	// "context canceled"。与 perf 路径同一套处理方式。
	guardedCtx, cancelGuard := context.WithCancelCause(ctx)
	defer cancelGuard(nil)
	unregister, err := defaultResourceGuard.Register(
		req.RunDir,
		time.Now(),
		resourceGuardPolicyFromConfig(conf, req.RunDir),
		cancelGuard,
	)
	if err != nil {
		result.Message = err.Error()
		return result, err
	}
	defer func() {
		unregister()
		guardErr := resourceGuardError(context.Cause(guardedCtx))
		if guardErr == nil {
			return
		}
		result.Success = false
		result.Message = guardErr.Error()
		returnErr = guardErr
	}()

	logger.Infof("[active_profile] fetch go pprof url=%s timeout=%s", endpoint, timeout)
	fetchCtx, cancelFetch := context.WithTimeout(guardedCtx, timeout)
	defer cancelFetch()
	payload, err := fetchGoPprof(fetchCtx, endpoint)
	if err != nil {
		return result, MarkError(ErrGoEndpoint, err)
	}
	payload, release, err := reserveGoPprofParse(req.RunDir, endpoint, payload)
	if err != nil {
		return result, err
	}
	defer release()

	profile, err := pprofprofile.ParseData(payload)
	if err != nil {
		return result, MarkError(ErrGoEndpoint, fmt.Errorf("parse go pprof payload from %s: %w", endpoint, err))
	}
	if len(profile.Sample) == 0 {
		return result, MarkError(
			ErrEmptyProfile,
			fmt.Errorf("go pprof endpoint %s returned a profile without samples", endpoint),
		)
	}
	labelGoProfileSamples(profile, conf, req)

	// endpoint 返回的是 gzip 的 pprof，这里重新序列化成未压缩 protobuf：上报载荷要求
	// 的就是这个形态。
	var serialized bytes.Buffer
	if err := profile.WriteUncompressed(&serialized); err != nil {
		return result, fmt.Errorf("serialize go pprof from %s: %w", endpoint, err)
	}

	result.Success = true
	result.ProfileData = serialized.Bytes()
	result.ProfileBytes = int64(serialized.Len())
	result.SampleCount = int64(len(profile.Sample))
	// go_cpu 是 cpu，go_heap 是 space；由产物自报，不按 collection_type 推断。
	if profile.PeriodType != nil {
		result.PeriodType = profile.PeriodType.Type
	}
	if req.GoEndpointProcess != nil {
		result.CollectedPIDs = []int32{req.GoEndpointProcess.PID}
	}

	// keep_artifacts 是受控调试开关，此时才留一份产物便于事后检查。
	if conf.KeepArtifacts {
		if strings.TrimSpace(req.RunDir) == "" {
			return result, fmt.Errorf("output directory is required when keep_artifacts is enabled")
		}
		profilePath := filepath.Join(req.RunDir, "profile.pprof")
		if err := os.WriteFile(profilePath, result.ProfileData, 0o600); err != nil {
			return result, fmt.Errorf("write go profile %s: %w", profilePath, err)
		}
		result.ProfilePath = profilePath
	}
	return result, nil
}

// goPprofEndpoint 按采集类型生成请求地址与该请求的时间预算。
func goPprofEndpoint(conf *configs.ActiveProfileTaskConfig) (string, time.Duration, error) {
	collector := conf.CollectorConfig
	// JoinHostPort 负责 IPv6 的方括号形式，避免手工拼接产生非法地址。
	host := net.JoinHostPort(collector.IPAddress, strconv.Itoa(collector.Port))
	endpoint := url.URL{Scheme: "http", Host: host}

	switch conf.CollectionType {
	case configs.ActiveProfileTypeGoCPU:
		endpoint.Path = goPprofCPUPath
		endpoint.RawQuery = url.Values{"seconds": {strconv.Itoa(collector.DurationSec)}}.Encode()
		return endpoint.String(), time.Duration(collector.DurationSec)*time.Second + goPprofFetchSlack, nil
	case configs.ActiveProfileTypeGoHeap:
		endpoint.Path = goPprofHeapPath
		return endpoint.String(), goPprofHeapTimeout, nil
	default:
		return "", 0, MarkError(ErrInvalidConfig, fmt.Errorf("unsupported golang collection_type %q", conf.CollectionType))
	}
}

func fetchGoPprof(ctx context.Context, endpoint string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create go pprof request for %s: %w", endpoint, err)
	}
	response, err := goPprofClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch go pprof from %s: %w", endpoint, err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, goPprofErrorDetailLimit))
		return nil, fmt.Errorf(
			"go pprof endpoint %s returned %s: %s",
			endpoint, response.Status, strings.TrimSpace(string(detail)),
		)
	}
	// 多读一个字节用于区分"正好等于上限"和"超过上限"。
	payload, err := io.ReadAll(io.LimitReader(response.Body, goPprofResponseLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read go pprof response from %s: %w", endpoint, err)
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("go pprof endpoint %s returned no data: %w", endpoint, ErrEmptyProfile)
	}
	if len(payload) > goPprofResponseLimit {
		return nil, fmt.Errorf(
			"go pprof response from %s exceeds the %d byte limit",
			endpoint, goPprofResponseLimit,
		)
	}
	return payload, nil
}

// reserveGoPprofParse 在开启内存保护时按剩余内存解压响应体，并在进程级账本里占住解析所需
// 的内存，返回可直接交给 ParseData 的 pprof 与释放函数。ParseData 不可中途打断，只能在
// 解析之前拒绝。未开启内存保护时原样返回。
func reserveGoPprofParse(runID, endpoint string, payload []byte) ([]byte, func(), error) {
	headroom, protected, err := defaultResourceGuard.memoryHeadroom(runID)
	if err != nil || !protected {
		return payload, func() {}, err
	}
	var limit uint64
	if headroom > goPprofParseBaseBytes {
		limit = (headroom - goPprofParseBaseBytes) / goPprofParseFactor
	}
	data := payload
	if len(payload) >= 2 && payload[0] == 0x1f && payload[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, func() {}, MarkError(ErrGoEndpoint, fmt.Errorf("decompress go pprof from %s: %w", endpoint, err))
		}
		data, err = io.ReadAll(io.LimitReader(reader, int64(limit)+1))
		if err != nil {
			return nil, func() {}, MarkError(ErrGoEndpoint, fmt.Errorf("decompress go pprof from %s: %w", endpoint, err))
		}
	}
	if uint64(len(data)) > limit {
		return nil, func() {}, MarkError(ErrMemoryLimit, fmt.Errorf(
			"go pprof from %s is too large to parse within the %d MB of memory left for profiling",
			endpoint, headroom/bytesPerMB,
		))
	}
	estimate := goPprofParseBaseBytes + goPprofParseFactor*uint64(len(data))
	release, err := defaultResourceGuard.reserveMemory(runID, estimate)
	if err != nil {
		return nil, func() {}, fmt.Errorf("parse go pprof from %s needs about %d MB: %w",
			endpoint, estimate/bytesPerMB, err)
	}
	return data, release, nil
}

// labelGoProfileSamples 给 Go 产物补上与 perf 路径完全一致的执行定位标签。pid/comm
// 只在按 endpoint 端口反查到唯一监听进程时写入；反查不到时保持标签不存在，不写占位
// 值。目标进程自己用 pprof.Do 打的标签一并保留。
func labelGoProfileSamples(profile *pprofprofile.Profile, conf *configs.ActiveProfileTaskConfig, req Request) {
	base := newBaseSampleLabels(SampleLabels{
		BizID:       conf.GetBizID(),
		TaskID:      conf.GetTaskID(),
		ScheduledAt: req.ScheduledAt.UnixMilli(),
		BKHostID:    conf.BKHostID,
		IP:          req.IP,
	})
	if req.GoEndpointProcess != nil {
		base["pid"] = []string{strconv.FormatInt(int64(req.GoEndpointProcess.PID), 10)}
		base["comm"] = []string{req.GoEndpointProcess.Comm}
	}
	for _, sample := range profile.Sample {
		if sample.Label == nil {
			sample.Label = make(map[string][]string, len(base))
		}
		for key, value := range base {
			sample.Label[key] = value
		}
	}
}
