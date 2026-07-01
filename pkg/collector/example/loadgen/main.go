// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// loadgen 是限流验收用的压测客户端，只依赖标准库，单独编译成二进制。
// 它向 OTLP HTTP /v1/traces 串行跑 warmup → burst → bigpayload 三个阶段，逐阶段打印各状态码计数
// 与成功请求 p99（429 是限流丢弃，503 仅作旁路异常观测），用来观察限流是否如预期分级、熔断。
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// OTLP span kind / status code 枚举值，取自 opentelemetry.proto.trace.v1。
	spanKindServer = 2
	spanKindClient = 3

	statusCodeOK    = 1
	statusCodeError = 2

	serviceName       = "collector-loadgen"
	syntheticErrorMsg = "loadgen synthetic error"

	// 单条 span 耗时在 [spanDurationMin, spanDurationMax] 区间均匀随机。
	spanDurationMin = 200 * time.Millisecond
	spanDurationMax = 500 * time.Millisecond

	// 每条 span 独立掷骰，命中 1/errorOneIn 注入 ERROR（≈ 5%）。
	errorOneIn = 20
)

// spanNames 是 SpanName 池，每条 span 均匀随机抽取一个，模拟服务实际接口分布。
var spanNames = []string{
	"/benchmark_one",
	"/benchmark_two",
	"/benchmark_three",
	"/benchmark_four",
	"/benchmark_five",
}

// 以下结构体字段名对齐 opentelemetry.proto 的 ExportTraceServiceRequest / Span，
// 用结构体 + json.Marshal 取代手拼字符串，便于扩展、阅读。
// startTimeUnixNano / endTimeUnixNano 在 OTLP/JSON 中按 proto3 fixed64 规范序列化为字符串。

type traceRequest struct {
	ResourceSpans []resourceSpans `json:"resourceSpans"`
}

type resourceSpans struct {
	Resource   resource     `json:"resource"`
	ScopeSpans []scopeSpans `json:"scopeSpans"`
}

type resource struct {
	Attributes []attribute `json:"attributes"`
}

type scopeSpans struct {
	Spans []span `json:"spans"`
}

type span struct {
	TraceID           string      `json:"traceId"`
	SpanID            string      `json:"spanId"`
	Name              string      `json:"name"`
	Kind              int         `json:"kind"`
	StartTimeUnixNano int64       `json:"startTimeUnixNano,string"`
	EndTimeUnixNano   int64       `json:"endTimeUnixNano,string"`
	Attributes        []attribute `json:"attributes"`
	Status            status      `json:"status"`
}

type attribute struct {
	Key   string         `json:"key"`
	Value attributeValue `json:"value"`
}

type attributeValue struct {
	StringValue string `json:"stringValue"`
}

type status struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

// phase 是一个压测阶段的参数。
type phase struct {
	name            string
	concurrency     int
	spansPerRequest int
	payloadSize     int
	duration        time.Duration
}

// stats 汇总一个阶段的结果：各状态码计数、成功请求的延迟样本与阶段起止时间。
type stats struct {
	mu        sync.Mutex
	status    map[int]int
	other     map[string]int
	latencies []time.Duration
	startedAt time.Time
	endedAt   time.Time
}

type requestResult struct {
	code  int
	other string
}

func main() {
	targetURL := flag.String("url", "http://127.0.0.1:4318/v1/traces", "target OTLP HTTP traces URL")
	token := flag.String("token", "", "X-BK-TOKEN header value")
	concurrency := flag.Int("c", 50, "burst and bigpayload concurrency")
	duration := flag.Duration("d", 30*time.Second, "duration per phase")
	warmupSpans := flag.Int("warmup-spans", 32, "warmup phase spans per request")
	burstSpans := flag.Int("burst-spans", 128, "burst phase spans per request")
	bigpayloadSpans := flag.Int("bigpayload-spans", 512, "bigpayload phase spans per request")
	flag.Parse()

	client := &http.Client{Timeout: 10 * time.Second}
	// 三阶段串行：warmup 低并发小 batch + 128 B 填充建基线，
	// burst 高并发中 batch + 1024 B 填充压 CPU，
	// bigpayload 并发不变、大 batch + 4096 B 填充，抬高单请求总成本。
	// 每阶段 spans 数量可通过 -warmup-spans / -burst-spans / -bigpayload-spans 覆盖默认值。
	phases := []phase{
		{name: "warmup", concurrency: max(1, *concurrency/5), spansPerRequest: *warmupSpans, payloadSize: 128, duration: *duration},
		{name: "burst", concurrency: *concurrency, spansPerRequest: *burstSpans, payloadSize: 1024, duration: *duration},
		{name: "bigpayload", concurrency: *concurrency, spansPerRequest: *bigpayloadSpans, payloadSize: 4096, duration: *duration},
	}

	overallStart := time.Now()
	fmt.Printf("loadgen start: %s\n", overallStart.Format(time.RFC3339))

	for _, p := range phases {
		result := runPhase(client, *targetURL, *token, p)
		printPhase(p, result)
	}

	overallEnd := time.Now()
	fmt.Printf("loadgen end: %s elapsed=%s\n",
		overallEnd.Format(time.RFC3339),
		overallEnd.Sub(overallStart).Round(time.Second),
	)
}

// runPhase 用固定并发的 worker 在 duration 内持续打流，直到上下文超时。
// payload 串按 payloadSize 仅分配一次跨请求复用，避免热点循环里反复 strings.Repeat。
func runPhase(client *http.Client, targetURL, token string, p phase) *stats {
	ctx, cancel := context.WithTimeout(context.Background(), p.duration)
	defer cancel()

	payload := strings.Repeat("x", p.payloadSize)
	result := &stats{status: make(map[int]int), other: make(map[string]int), startedAt: time.Now()}
	var wg sync.WaitGroup
	for i := 0; i < p.concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				body := traceBody(p.spansPerRequest, payload)
				start := time.Now()
				res := post(ctx, client, targetURL, token, body)
				// 阶段窗口已关闭、且本次请求是网络错误：是 ctx 主动取消而非 collector 异常，跳过统计避免污染 other 桶。
				if res.code == 0 && ctx.Err() != nil {
					return
				}
				result.add(res, time.Since(start))
			}
		}()
	}
	wg.Wait()
	result.endedAt = time.Now()
	return result
}

// post 发一条 trace，丢弃响应体只取状态码；网络错误归类后计入 other 明细。
func post(ctx context.Context, client *http.Client, targetURL, token string, body []byte) requestResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return requestResult{other: "request_build_error"}
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-BK-TOKEN", token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return requestResult{other: classifyError(err)}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return requestResult{code: resp.StatusCode}
}

func (s *stats) add(res requestResult, latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if res.code == 0 {
		if res.other == "" {
			res.other = "network_error"
		}
		s.other[res.other]++
		return
	}

	s.status[res.code]++
	if res.code >= 200 && res.code < 300 {
		s.latencies = append(s.latencies, latency)
	}
}

func printPhase(p phase, s *stats) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fmt.Printf("phase=%s concurrency=%d spans=%d duration=%s start=%s end=%s\n",
		p.name, p.concurrency, p.spansPerRequest, p.duration,
		s.startedAt.Format(time.RFC3339),
		s.endedAt.Format(time.RFC3339),
	)
	fmt.Printf("  200=%d 429=%d 503=%d other=%d success_p99=%s\n",
		s.status[http.StatusOK],
		s.status[http.StatusTooManyRequests],
		s.status[http.StatusServiceUnavailable],
		otherCount(s.status, s.other),
		p99(s.latencies),
	)
	if detail := otherDetail(s.status, s.other); detail != "" {
		fmt.Printf("  other_detail=%s\n", detail)
	}
}

func otherCount(status map[int]int, other map[string]int) int {
	total := 0
	for code, n := range status {
		switch code {
		case http.StatusOK, http.StatusTooManyRequests, http.StatusServiceUnavailable:
			continue
		}
		total += n
	}
	for _, n := range other {
		total += n
	}
	return total
}

func otherDetail(status map[int]int, other map[string]int) string {
	var items []string
	for code, n := range status {
		switch code {
		case http.StatusOK, http.StatusTooManyRequests, http.StatusServiceUnavailable:
			continue
		}
		items = append(items, fmt.Sprintf("status_%d=%d", code, n))
	}
	for name, n := range other {
		items = append(items, fmt.Sprintf("%s=%d", name, n))
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}

func classifyError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns_error"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}

	switch {
	case errors.Is(err, io.EOF):
		return "eof"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection_reset"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "connection reset"):
		return "connection_reset"
	case strings.Contains(msg, "connection refused"):
		return "connection_refused"
	case strings.Contains(msg, "broken pipe"):
		return "broken_pipe"
	case strings.Contains(msg, "eof"):
		return "eof"
	case strings.Contains(msg, "timeout"):
		return "timeout"
	}
	return "network_error"
}

// p99 取成功延迟的 99 分位。样本已是单阶段数据，排序后直接取下标。
func p99(latencies []time.Duration) time.Duration {
	if len(latencies) == 0 {
		return 0
	}
	items := append([]time.Duration(nil), latencies...)
	sort.Slice(items, func(i, j int) bool { return items[i] < items[j] })
	idx := int(float64(len(items)-1) * 0.99)
	return items[idx]
}

// traceBody 构造一次 OTLP HTTP/JSON 请求体，单请求内含 spansPerRequest 条独立 span。
// 所有 span 共享同一个 service.name resource，但各自独立随机
// traceId / spanId / name / kind / duration / status。
func traceBody(spansPerRequest int, payload string) []byte {
	spans := make([]span, spansPerRequest)
	for i := range spans {
		spans[i] = newSpan(i, payload)
	}
	body, _ := json.Marshal(traceRequest{
		ResourceSpans: []resourceSpans{{
			Resource: resource{
				Attributes: []attribute{
					{Key: "service.name", Value: attributeValue{StringValue: serviceName}},
				},
			},
			ScopeSpans: []scopeSpans{{Spans: spans}},
		}},
	})
	return body
}

// newSpan 生成一条 span：
//   - 偶数索引 → SERVER（被调），奇数 → CLIENT（主调），固定 50:50；
//   - 命中 1/errorOneIn 注入 ERROR，否则 OK；
//   - 耗时在 [spanDurationMin, spanDurationMax] 均匀随机，endTime 对齐到生成时刻；
//   - SpanName 从 spanNames 池均匀随机抽取。
func newSpan(index int, payload string) span {
	traceID := newTraceID()
	spanID := newSpanID()

	kind := spanKindServer
	if index%2 == 1 {
		kind = spanKindClient
	}

	st := status{Code: statusCodeOK}
	if rand.IntN(errorOneIn) == 0 {
		st = status{Code: statusCodeError, Message: syntheticErrorMsg}
	}

	dur := spanDurationMin + time.Duration(rand.Int64N(int64(spanDurationMax-spanDurationMin)+1))
	end := time.Now()
	start := end.Add(-dur)

	return span{
		TraceID:           hex.EncodeToString(traceID[:]),
		SpanID:            hex.EncodeToString(spanID[:]),
		Name:              spanNames[rand.IntN(len(spanNames))],
		Kind:              kind,
		StartTimeUnixNano: start.UnixNano(),
		EndTimeUnixNano:   end.UnixNano(),
		Attributes: []attribute{
			{Key: "payload", Value: attributeValue{StringValue: payload}},
		},
		Status: st,
	}
}

// newTraceID 生成 128 bit 随机 traceId，对齐 OpenTelemetry SDK randomIDGenerator 的 NewIDs。
// W3C Trace Context 规定全 0 是无效 traceId，命中后重试（概率 2^-128，几乎不会发生）。
func newTraceID() [16]byte {
	var id [16]byte
	for {
		binary.BigEndian.PutUint64(id[0:8], rand.Uint64())
		binary.BigEndian.PutUint64(id[8:16], rand.Uint64())
		if id != ([16]byte{}) {
			return id
		}
	}
}

// newSpanID 生成 64 bit 随机 spanId，对齐 OpenTelemetry SDK randomIDGenerator 的 NewSpanID。
// W3C Trace Context 规定全 0 是无效 spanId，命中后重试（概率 2^-64）。
func newSpanID() [8]byte {
	var id [8]byte
	for {
		binary.BigEndian.PutUint64(id[:], rand.Uint64())
		if id != ([8]byte{}) {
			return id
		}
	}
}
