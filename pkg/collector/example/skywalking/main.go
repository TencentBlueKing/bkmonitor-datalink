// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/SkyAPM/go2sky"
	"github.com/SkyAPM/go2sky/reporter"
)

const (
	endpoint = "127.0.0.1:4317"
	token    = "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw=="
)

var exporter go2sky.Reporter

func init() {
	// 定义上报的地址以及携带的 Token 数据
	r, err := reporter.NewGRPCReporter(endpoint, reporter.WithAuthentication(token))
	if err != nil {
		log.Fatalf("failed to init grpc reporter, err: %v\n", err)
	}
	// 全局变量 exporter 赋值
	exporter = r
}

func add(x, y int, ctx context.Context) (int, error) {
	trace := go2sky.GetGlobalTracer()
	span, _, _ := trace.CreateLocalSpan(ctx)
	span.SetOperationName("addFunc")
	span.End()
	time.Sleep(time.Second)

	res := x + y
	return res, nil
}

func startCalculate(ctx context.Context) {
	trace := go2sky.GetGlobalTracer()
	span, ctx, _ := trace.CreateLocalSpan(ctx)
	span.SetOperationName("startCalculateFunc")
	span.End()
	time.Sleep(time.Second)
	res, _ := add(100, 10, ctx)
	log.Printf("addFunction Result is %d\n", res)
}

func initTrace(serverInstance string) {
	trace, err := go2sky.NewTracer(
		serverInstance,
		go2sky.WithSampler(1),
		go2sky.WithReporter(exporter),
	)
	if err != nil {
		log.Printf("NewTracer Error %s\n", err)
		return
	}

	// 设定全局trace
	go2sky.SetGlobalTracer(trace)
	span, ctx, err := trace.CreateLocalSpan(context.Background())
	if err != nil {
		log.Printf("initTrace CreateLocalSpan Error %s\n", err)
		return
	}
	span.SetOperationName("initTraceFunc")

	// 调用模拟调用链路函数 上报 trace 数据
	startCalculate(ctx)
	span.End()
	time.Sleep(time.Second) // 等待数据上报完成
}

func main() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGINT)

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	count := 0
	for {
		select {
		case <-c:
			return
		case <-ticker.C:
			count += 1
			serverInstance := "TestGrpc_" + strconv.Itoa(count)
			initTrace(serverInstance)
		}
	}
}
