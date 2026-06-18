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
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// phase 是一个压测阶段的参数。
type phase struct {
	name        string
	concurrency int
	payloadSize int
	duration    time.Duration
}

// stats 汇总一个阶段的结果：各状态码计数与成功请求的延迟样本。
type stats struct {
	mu        sync.Mutex
	status    map[int]int
	latencies []time.Duration
}

func main() {
	targetURL := flag.String("url", "http://127.0.0.1:4318/v1/traces", "target OTLP HTTP traces URL")
	token := flag.String("token", "", "X-BK-TOKEN header value")
	concurrency := flag.Int("c", 50, "burst and bigpayload concurrency")
	duration := flag.Duration("d", 30*time.Second, "duration per phase")
	flag.Parse()

	client := &http.Client{Timeout: 10 * time.Second}
	// 三阶段串行：warmup 低并发小包建基线，burst 高并发压 CPU，bigpayload 并发不变、单包放大到 64 倍（128→8192）抬高单请求成本。
	phases := []phase{
		{name: "warmup", concurrency: max(1, *concurrency/5), payloadSize: 128, duration: *duration},
		{name: "burst", concurrency: *concurrency, payloadSize: 128, duration: *duration},
		{name: "bigpayload", concurrency: *concurrency, payloadSize: 8192, duration: *duration},
	}

	for _, p := range phases {
		result := runPhase(client, *targetURL, *token, p)
		printPhase(p, result)
	}
}

// runPhase 用固定并发的 worker 在 duration 内持续打流，直到上下文超时。
func runPhase(client *http.Client, targetURL, token string, p phase) *stats {
	ctx, cancel := context.WithTimeout(context.Background(), p.duration)
	defer cancel()

	result := &stats{status: make(map[int]int)}
	var seq uint64
	var wg sync.WaitGroup
	for i := 0; i < p.concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				id := atomic.AddUint64(&seq, 1)
				body := traceBody(id, p.payloadSize)
				start := time.Now()
				code := post(ctx, client, targetURL, token, body)
				result.add(code, time.Since(start))
			}
		}()
	}
	wg.Wait()
	return result
}

// post 发一条 trace，丢弃响应体只取状态码；网络错误记为 0。
func post(ctx context.Context, client *http.Client, targetURL, token, body string) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader([]byte(body)))
	if err != nil {
		return 0
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-BK-TOKEN", token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func (s *stats) add(code int, latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[code]++
	if code >= 200 && code < 300 {
		s.latencies = append(s.latencies, latency)
	}
}

func printPhase(p phase, s *stats) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fmt.Printf("phase=%s concurrency=%d duration=%s\n", p.name, p.concurrency, p.duration)
	fmt.Printf("  200=%d 429=%d 503=%d other=%d success_p99=%s\n",
		s.status[http.StatusOK],
		s.status[http.StatusTooManyRequests],
		s.status[http.StatusServiceUnavailable],
		otherStatusCount(s.status),
		p99(s.latencies),
	)
}

func otherStatusCount(status map[int]int) int {
	total := 0
	for code, n := range status {
		if code == http.StatusOK || code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable {
			continue
		}
		total += n
	}
	return total
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

// traceBody 拼一条最小 OTLP JSON trace，用 payloadSize 个字节的填充串放大单包成本。
func traceBody(id uint64, payloadSize int) string {
	payload := strings.Repeat("x", payloadSize)
	now := time.Now().UnixNano()
	return fmt.Sprintf(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"collector-loadgen"}}]},"scopeSpans":[{"spans":[{"traceId":"%032x","spanId":"%016x","name":"loadgen","startTimeUnixNano":"%d","endTimeUnixNano":"%d","attributes":[{"key":"payload","value":{"stringValue":"%s"}}]}]}]}]}`,
		id, id, now, now+int64(time.Millisecond), payload,
	)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
