// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// trafficload 向 OTLP HTTP /v1/traces 发送 Protobuf traces，按计费字节控制到达形态：
// 均匀、每秒前 100ms 突发、1ms 尖峰、以及三实例切流。
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

type outcome struct {
	ok        atomic.Int64
	rejected  atomic.Int64
	other     atomic.Int64
	lastOther atomic.Int64
}

func (o *outcome) add(code int) {
	switch code {
	case http.StatusOK:
		o.ok.Add(1)
	case http.StatusTooManyRequests:
		o.rejected.Add(1)
	default:
		o.other.Add(1)
		o.lastOther.Store(int64(code))
	}
}

func (o *outcome) snapshot() (ok, rejected, other int64) {
	return o.ok.Load(), o.rejected.Load(), o.other.Load()
}

func main() {
	urlsFlag := flag.String("urls", "http://127.0.0.1:4318/v1/traces", "comma-separated OTLP HTTP traces URLs")
	scenario := flag.String("scenario", "burst100", "once|steady-under|steady-over|burst100|burst-over|spike|spike-over|instant|instant-over|release-switch")
	limit := flag.Int64("limit", 1024*1024, "collector bytes_per_second")
	dumpBytes := flag.Int64("dump-bytes", 0, "instant/burst dump size; 0 means use -limit")
	duration := flag.Duration("d", 8*time.Second, "total duration")
	concurrency := flag.Int("c", 64, "in-flight requests per target")
	service := flag.String("service", "checkout", "resource service.name")
	spans := flag.Int("spans", 20, "spans per request")
	pad := flag.Int("pad", 8192, "resource attribute padding bytes")
	switchAt := flag.Duration("switch-at", 4*time.Second, "release-switch: send only to the first URL after this")
	window := flag.Duration("window", 100*time.Millisecond, "burst window at the start of each second")
	flag.Parse()

	body, logical := buildRequest(*service, *spans, *pad)
	dump := *dumpBytes
	if dump <= 0 {
		dump = *limit
	}
	fmt.Printf("payload logical=%dB wire=%dB spans=%d pad=%d service=%s scenario=%s limit=%d dump=%d\n",
		logical, len(body), *spans, *pad, *service, *scenario, *limit, dump)

	urls := splitCSV(*urlsFlag)
	if len(urls) == 0 {
		fmt.Fprintln(os.Stderr, "no urls")
		os.Exit(2)
	}
	clients := make([]*http.Client, len(urls))
	for i := range urls {
		clients[i] = newClient()
	}

	var failed bool
	switch *scenario {
	case "once":
		failed = runOnce(urls[0], clients[0], body, logical)
	case "steady-under":
		failed = runSteady(urls, clients, body, logical, *limit/2, *concurrency, *duration, true)
	case "steady-over":
		failed = runSteady(urls, clients, body, logical, *limit*3, *concurrency, *duration, false)
	case "burst100":
		failed = runBurst(urls, clients, body, logical, dump, *concurrency, *duration, *window, true)
	case "burst-over":
		failed = runBurst(urls, clients, body, logical, *limit*3, *concurrency, *duration, *window, false)
	case "spike":
		failed = runSpike(urls[0], clients[0], body, logical, *limit*2/3, *concurrency, *duration, true)
	case "spike-over":
		failed = runSpike(urls[0], clients[0], body, logical, *limit*2, *concurrency, *duration, false)
	case "instant":
		failed = runInstant(urls, clients, body, logical, dump, *concurrency, true)
	case "instant-over":
		failed = runInstant(urls, clients, body, logical, dump, *concurrency, false)
	case "release-switch":
		failed = runReleaseSwitch(urls, clients, body, logical, *limit, *concurrency, *duration, *window, *switchAt)
	default:
		fmt.Fprintf(os.Stderr, "unknown scenario %q\n", *scenario)
		os.Exit(2)
	}
	if failed {
		os.Exit(1)
	}
}

func newClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        512,
			MaxIdleConnsPerHost: 256,
			DisableCompression:  true,
		},
	}
}

func splitCSV(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func buildRequest(service string, spans, pad int) ([]byte, int64) {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutString("service.name", service)
	if pad > 0 {
		rs.Resource().Attributes().PutString("pad", strings.Repeat("x", pad))
	}
	now := pcommon.NewTimestampFromTime(time.Unix(1700000000, 0))
	scope := rs.ScopeSpans().AppendEmpty()
	for i := 0; i < spans; i++ {
		span := scope.Spans().AppendEmpty()
		span.SetName("otlp-bench")
		span.SetTraceID(pcommon.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, byte(i)})
		span.SetSpanID(pcommon.SpanID{1, 2, 3, 4, 5, 6, 7, byte(i)})
		span.SetStartTimestamp(now)
		span.SetEndTimestamp(now)
		span.SetKind(ptrace.SpanKindServer)
	}
	sizer := ptrace.NewProtoMarshaler().(ptrace.Sizer)
	logical := int64(sizer.TracesSize(td))
	body, err := ptraceotlp.NewRequestFromTraces(td).MarshalProto()
	if err != nil {
		panic(err)
	}
	return body, logical
}

var unexpectedOnce sync.Once

func post(client *http.Client, url string, body []byte) int {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		unexpectedOnce.Do(func() { fmt.Fprintf(os.Stderr, "first request error: %v\n", err) })
		return 0
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := client.Do(req)
	if err != nil {
		unexpectedOnce.Do(func() { fmt.Fprintf(os.Stderr, "first request error: %v\n", err) })
		return 0
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusTooManyRequests {
		unexpectedOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "first unexpected status=%d body=%q\n", resp.StatusCode, respBody)
		})
	}
	return resp.StatusCode
}

func sendN(client *http.Client, url string, body []byte, n, conc int) (outcome, time.Duration) {
	if n <= 0 {
		return outcome{}, 0
	}
	var (
		next atomic.Int64
		out  outcome
		wg   sync.WaitGroup
	)
	start := time.Now()
	workers := min(conc, n)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for next.Add(1) <= int64(n) {
				out.add(post(client, url, body))
			}
		}()
	}
	wg.Wait()
	return out, time.Since(start)
}

func printWindow(label string, elapsed time.Duration, out outcome, bytes int64) (rejected int64) {
	ok, rejected, other := out.snapshot()
	rate := 0.0
	if elapsed > 0 {
		rate = float64(bytes) / elapsed.Seconds()
	}
	fmt.Printf("%s elapsed=%s 200=%d 429=%d other=%d last_other=%d bytes=%d dump=%.0fB/s\n",
		label, elapsed.Round(time.Millisecond), ok, rejected, other, out.lastOther.Load(), bytes, rate)
	return rejected
}

func runOnce(url string, client *http.Client, body []byte, logical int64) bool {
	out, elapsed := sendN(client, url, body, 1, 1)
	printWindow("once", elapsed, out, logical)
	ok, rejected, other := out.snapshot()
	if rejected > 0 || other > 0 || ok != 1 {
		fmt.Println("FAIL warmup/once expected a single 200")
		return true
	}
	fmt.Println("PASS")
	return false
}

func runInstant(urls []string, clients []*http.Client, body []byte, logical, dump int64, conc int, wantZeroReject bool) bool {
	n := int(dump / logical)
	if n < 1 {
		n = 1
	}
	perURL := n / len(urls)
	if perURL < 1 {
		perURL = 1
	}
	var wg sync.WaitGroup
	outs := make([]outcome, len(urls))
	elapseds := make([]time.Duration, len(urls))
	start := time.Now()
	for i := range urls {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outs[i], elapseds[i] = sendN(clients[i], urls[i], body, perURL, conc)
		}(i)
	}
	wg.Wait()
	var merged outcome
	for i := range urls {
		ok, rejected, other := outs[i].snapshot()
		merged.ok.Add(ok)
		merged.rejected.Add(rejected)
		merged.other.Add(other)
		if outs[i].lastOther.Load() != 0 {
			merged.lastOther.Store(outs[i].lastOther.Load())
		}
	}
	totalBytes := int64(perURL*len(urls)) * logical
	printWindow("instant", time.Since(start), merged, totalBytes)
	rejected := merged.rejected.Load()
	if wantZeroReject && rejected > 0 {
		fmt.Printf("FAIL expected 429=0 for in-limit dump, got %d\n", rejected)
		return true
	}
	if !wantZeroReject && rejected == 0 {
		fmt.Println("FAIL expected 429>0 when dump exceeds capacity")
		return true
	}
	fmt.Println("PASS")
	return false
}

func runSteady(urls []string, clients []*http.Client, body []byte, logical, rate int64, conc int, d time.Duration, wantZeroReject bool) bool {
	nPerSec := int(rate / logical)
	if nPerSec < 1 {
		nPerSec = 1
	}
	interval := time.Second / time.Duration(nPerSec)
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var total outcome
	start := time.Now()
	var wg sync.WaitGroup
	for i, url := range urls {
		wg.Add(1)
		go func(url string, client *http.Client) {
			defer wg.Done()
			ticker := time.NewTicker(interval * time.Duration(len(urls)))
			defer ticker.Stop()
			total.add(post(client, url, body))
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					total.add(post(client, url, body))
				}
			}
		}(url, clients[i])
	}
	wg.Wait()
	ok, rejected, other := total.snapshot()
	elapsed := time.Since(start)
	fmt.Printf("summary 200=%d 429=%d other=%d elapsed=%s admitted=%.0fB/s\n",
		ok, rejected, other, elapsed.Round(time.Millisecond), float64(ok)*float64(logical)/elapsed.Seconds())
	if wantZeroReject && rejected > 0 {
		fmt.Println("FAIL expected 429=0")
		return true
	}
	if !wantZeroReject && rejected == 0 {
		fmt.Println("FAIL expected some 429 under over-limit load")
		return true
	}
	fmt.Println("PASS")
	return false
}

func runBurst(urls []string, clients []*http.Client, body []byte, logical, rate int64, conc int, d time.Duration, window time.Duration, wantZeroReject bool) bool {
	seconds := int(d / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	perURL := int(rate/logical) / len(urls)
	if perURL < 1 {
		perURL = 1
	}
	var failed bool
	var totalReject int64
	for sec := 0; sec < seconds; sec++ {
		secStart := time.Now()
		var wg sync.WaitGroup
		outs := make([]outcome, len(urls))
		elapseds := make([]time.Duration, len(urls))
		for i := range urls {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				outs[i], elapseds[i] = sendN(clients[i], urls[i], body, perURL, conc)
			}(i)
		}
		wg.Wait()
		var merged outcome
		var maxElapsed time.Duration
		for i := range urls {
			ok, rejected, other := outs[i].snapshot()
			merged.ok.Add(ok)
			merged.rejected.Add(rejected)
			merged.other.Add(other)
			if elapseds[i] > maxElapsed {
				maxElapsed = elapseds[i]
			}
		}
		totalReject += printWindow(fmt.Sprintf("sec=%d burst", sec), maxElapsed, merged, int64(perURL*len(urls))*logical)
		if wantZeroReject && merged.rejected.Load() > 0 {
			failed = true
		}
		if sleep := time.Second - time.Since(secStart); sleep > 0 {
			time.Sleep(sleep)
		}
		_ = window
	}
	if wantZeroReject && totalReject > 0 {
		fmt.Printf("FAIL expected 429=0, got %d\n", totalReject)
		return true
	}
	if !wantZeroReject && totalReject == 0 {
		fmt.Println("FAIL expected 429>0 when burst exceeds capacity")
		return true
	}
	fmt.Println("PASS")
	return failed
}

func runSpike(url string, client *http.Client, body []byte, logical, spikeBytes int64, conc int, d time.Duration, wantZeroReject bool) bool {
	n := int(spikeBytes / logical)
	if n < 1 {
		n = 1
	}
	seconds := int(d / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	var totalReject int64
	for sec := 0; sec < seconds; sec++ {
		out, elapsed := sendN(client, url, body, n, conc)
		totalReject += printWindow(fmt.Sprintf("sec=%d spike", sec), elapsed, out, int64(n)*logical)
		if sleep := time.Second - elapsed; sleep > 0 {
			time.Sleep(sleep)
		}
	}
	if wantZeroReject && totalReject > 0 {
		fmt.Printf("FAIL expected 429=0 for in-limit spike, got %d\n", totalReject)
		return true
	}
	if !wantZeroReject && totalReject == 0 {
		fmt.Println("FAIL expected 429>0 when spike exceeds capacity")
		return true
	}
	fmt.Println("PASS")
	return false
}

func runReleaseSwitch(urls []string, clients []*http.Client, body []byte, logical, limit int64, conc int, d, window, switchAt time.Duration) bool {
	if len(urls) < 1 {
		fmt.Println("FAIL need at least one URL")
		return true
	}
	seconds := int(d / time.Second)
	nAll := int(limit / logical)
	nShare := nAll / max(len(urls), 1)
	if nShare < 1 {
		nShare = 1
	}
	var failed bool
	start := time.Now()
	for sec := 0; sec < seconds; sec++ {
		secStart := time.Now()
		switched := time.Since(start) >= switchAt
		var (
			wg     sync.WaitGroup
			merged outcome
		)
		if switched {
			out, elapsed := sendN(clients[0], urls[0], body, nAll, conc)
			if printWindow(fmt.Sprintf("sec=%d switched->A", sec), elapsed, out, int64(nAll)*logical) > 0 {
				failed = true
			}
		} else {
			outs := make([]outcome, len(urls))
			elapseds := make([]time.Duration, len(urls))
			for i := range urls {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					outs[i], elapseds[i] = sendN(clients[i], urls[i], body, nShare, conc)
				}(i)
			}
			wg.Wait()
			var maxElapsed time.Duration
			for i := range urls {
				ok, rejected, other := outs[i].snapshot()
				merged.ok.Add(ok)
				merged.rejected.Add(rejected)
				merged.other.Add(other)
				if elapseds[i] > maxElapsed {
					maxElapsed = elapseds[i]
				}
			}
			if printWindow(fmt.Sprintf("sec=%d split", sec), maxElapsed, merged, int64(nShare*len(urls))*logical) > 0 {
				failed = true
			}
		}
		if sleep := time.Second - time.Since(secStart); sleep > 0 {
			time.Sleep(sleep)
		}
		_ = window
	}
	if failed {
		fmt.Println("FAIL release-switch saw 429 in a 1s burst window")
		return true
	}
	fmt.Println("PASS")
	return false
}
