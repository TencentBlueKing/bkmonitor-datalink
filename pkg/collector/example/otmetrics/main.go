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
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric"
	grpxexporter "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	httpexporter "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	stdout "go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/metric/global"
	controller "go.opentelemetry.io/otel/sdk/metric/controller/basic"
	processor "go.opentelemetry.io/otel/sdk/metric/processor/basic"
	"go.opentelemetry.io/otel/sdk/metric/selector/simple"
	"go.opentelemetry.io/otel/sdk/resource"
)

func newHttpExporter() (*otlpmetric.Exporter, error) {
	exporter, err := httpexporter.New(
		context.Background(),
		httpexporter.WithInsecure(),
		httpexporter.WithEndpoint("localhost:4318"),
	)
	return exporter, err
}

func newGrpcExporter() (*otlpmetric.Exporter, error) {
	exporter, err := grpxexporter.New(
		context.Background(),
		grpxexporter.WithInsecure(),
		grpxexporter.WithEndpoint("localhost:4317"),
	)
	return exporter, err
}

func newStdoutExporter() (*stdout.Exporter, error) {
	return stdout.New(stdout.WithPrettyPrint())
}

func main() {
	exporter, err := newGrpcExporter()
	if err != nil {
		log.Fatalln("failed to initialize metric exporter:", err)
	}
	token := "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw=="
	cont := controller.New(
		processor.NewFactory(
			simple.NewWithInexpensiveDistribution(),
			exporter,
		),
		controller.WithExporter(exporter),
		controller.WithCollectPeriod(3*time.Second),
		controller.WithResource(resource.NewSchemaless(attribute.String("bk.data.token", token))),
		controller.WithResource(resource.NewSchemaless(attribute.String("process.pid", "1024"))),
		controller.WithResource(resource.NewSchemaless(attribute.String("process.name", "hello-world"))),
	)
	if err := cont.Start(context.Background()); err != nil {
		log.Fatalln("failed to start the metric controller:", err)
	}
	global.SetMeterProvider(cont)

	if err := runtime.Start(
		runtime.WithMinimumReadMemStatsInterval(time.Second),
	); err != nil {
		log.Fatalln("failed to start runtime instrumentation:", err)
	}

	ctx, cancel := newOSSignalContext()
	defer cancel()

	meter := cont.Meter("mando.usage")

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		g1, _ := meter.AsyncInt64().Gauge("gauge_int")
		g2, _ := meter.AsyncFloat64().Gauge("gauge_float")

		for {
			select {
			case <-ticker.C:
				g1.Observe(context.Background(), 1, attribute.String("callee_service", "hello"), attribute.String("code", "ret_201"))
				g2.Observe(context.Background(), float64(time.Now().Second()))
			case <-ctx.Done():
				return
			}
		}
	}()

	<-ctx.Done()

	if err := cont.Stop(context.Background()); err != nil {
		log.Fatalln("failed to stop the metric controller:", err)
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
