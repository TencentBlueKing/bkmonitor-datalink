// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
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
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/consumer"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lifecycle"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	httpservice "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/service/http"
)

var (
	version       = "dev"
	commit        = "unknown"
	schemaVersion = "none"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	eventLogger := observability.New(observability.ComponentTrigger, stderr)
	return runWithDependencies(ctx, args, stdout, stderr, defaultApplicationDependencies(eventLogger))
}

func runWithDependencies(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	dependencies applicationDependencies,
) int {
	flags := flag.NewFlagSet("alarmd", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to alarmd YAML configuration")
	showVersion := flags.Bool("version", false, "print build information and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %v\n", flags.Args())
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "alarmd version=%s commit=%s schema_version=%s\n", version, commit, schemaVersion)
		return 0
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return 1
	}

	recorder := metric.NewRecorder(metric.BuildInfo{
		Version:       version,
		Commit:        commit,
		SchemaVersion: schemaVersion,
	})
	if err := runApplication(ctx, cfg, recorder, dependencies); err != nil {
		fmt.Fprintf(stderr, "run alarmd: %v\n", err)
		return 1
	}
	return 0
}

func defaultApplicationDependencies(eventLogger *observability.Logger) applicationDependencies {
	return applicationDependencies{
		logger: eventLogger,
		openSink: func(cfg config.KafkaConfig) (decisionSinkRuntime, error) {
			return enginekafka.OpenDecisionSink(cfg.DecisionSinkCoordinates())
		},
		openService: func(
			cfg config.KafkaConfig,
			newProcessor consumer.ProcessorFactory,
			drainTimeout time.Duration,
		) (serviceRuntime, error) {
			return enginekafka.OpenService(cfg.ConsumerCoordinates(), newProcessor, drainTimeout)
		},
		newHTTP: func(recorder *metric.Recorder, source lifecycle.Source) (httpRuntime, error) {
			return httpservice.NewWithLifecycle(recorder, source)
		},
	}
}
