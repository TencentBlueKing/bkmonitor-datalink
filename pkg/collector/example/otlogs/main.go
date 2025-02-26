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
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	stdout "go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

func newHttpExporter(endpoint, token string) (*otlploghttp.Exporter, error) {
	return otlploghttp.New(
		context.Background(),
		otlploghttp.WithInsecure(),
		otlploghttp.WithEndpoint(endpoint),
		otlploghttp.WithHeaders(map[string]string{"X-BK-TOKEN": token}),
	)
}

func newGrpcExporter(endpoint, token string) (*otlploggrpc.Exporter, error) {
	return otlploggrpc.New(
		context.Background(),
		otlploggrpc.WithInsecure(),
		otlploggrpc.WithEndpoint(endpoint),
		otlploggrpc.WithHeaders(map[string]string{"X-BK-TOKEN": token}),
	)
}

func newStdoutExporter() (*stdout.Exporter, error) {
	return stdout.New(stdout.WithPrettyPrint())
}

func main() {
	output := flag.String("exporter", "grpc", "output represents the standard exporter type, optional: stdout/http/grpc")
	token := flag.String("token", "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==", "authentication token")
	endpoint := flag.String("endpoint", "localhost:4317", "report endpoint")
	flag.Parse()

	var exporter sdklog.Exporter
	var err error
	switch *output {
	case "grpc":
		exporter, err = newGrpcExporter(*endpoint, *token)
	case "http":
		exporter, err = newHttpExporter(*endpoint, *token)
	default:
		exporter, err = newStdoutExporter()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize log exporter: %v\n", err)
		os.Exit(1)
	}

	cont := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		sdklog.WithResource(resource.NewSchemaless(attribute.String("process.name", "logger-example"))),
	)
	global.SetLoggerProvider(cont)

	ctx, cancel := newOSSignalContext()
	defer cancel()

	logger := otelslog.NewLogger("otlp-logger")
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		var count int
		for {
			select {
			case <-ticker.C:
				count++
				logger.Info("log from opentelemetry sdk", "count", count)

			case <-ctx.Done():
				return
			}
		}
	}()

	<-ctx.Done()

	if err := cont.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "failed to stop the log controller: %v\n", err)
		os.Exit(1)
	}
}

func newOSSignalContext() (context.Context, func()) {
	// trap Ctrl+C and call cancel on the context
	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		select {
		case <-c:
			cancel()
		case <-ctx.Done():
		}
	}()

	return ctx, func() {
		signal.Stop(c)
		cancel()
	}
}
