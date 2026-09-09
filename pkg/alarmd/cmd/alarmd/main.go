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
	"runtime"
	"syscall"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	httpservice "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/service/http"
)

var (
	version       = "dev"
	commit        = "unknown"
	schemaVersion = "none"
)

// Contention is the one dimension the profiles cannot answer with the defaults:
// mutex and block sampling are off unless the process turns them on. Both rates
// are deliberately coarse — one in a hundred contention events, and one blocking
// event per millisecond of blocking — so the samples identify which lock is
// contended without the sampling itself distorting the measurement.
const (
	mutexProfileFraction = 100
	blockProfileRateNS   = 1_000_000
)

func main() {
	runtime.SetMutexProfileFraction(mutexProfileFraction)
	runtime.SetBlockProfileRate(blockProfileRateNS)
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
	return runWithRuntimeModeDependencies(ctx, args, stdout, stderr, runtimeModeDependencies{
		phaseOne:                       dependencies,
		phaseTwo:                       defaultPhaseTwoApplicationDependencies(),
		temporaryLegacyDrainingCleanup: runTemporaryLegacyDrainingCleanup,
	})
}

func runWithRuntimeModeDependencies(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	dependencies runtimeModeDependencies,
) int {
	flags := flag.NewFlagSet("alarmd", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to alarmd YAML configuration")
	checkConfig := flags.Bool("check-config", false, "validate configuration and exit")
	showVersion := flags.Bool("version", false, "print build information and exit")
	temporaryCleanupRequest := flags.String(
		"temporary-admin-legacy-draining-cleanup-request",
		"",
		"path to the approved one-shot legacy Draining cleanup request JSON",
	)
	temporaryCleanupDigest := flags.String(
		"temporary-admin-legacy-draining-cleanup-apply-digest",
		"",
		"dry-run plan digest to apply; omit for dry-run",
	)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %v\n", flags.Args())
		return 2
	}
	if *temporaryCleanupDigest != "" && *temporaryCleanupRequest == "" {
		fmt.Fprintln(stderr, "--temporary-admin-legacy-draining-cleanup-apply-digest requires --temporary-admin-legacy-draining-cleanup-request")
		return 2
	}
	terminalModes := 0
	for _, enabled := range []bool{*showVersion, *checkConfig, *temporaryCleanupRequest != ""} {
		if enabled {
			terminalModes++
		}
	}
	if terminalModes > 1 {
		fmt.Fprintln(stderr, "--version, --check-config, and temporary legacy Draining cleanup cannot be used together")
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
	if *checkConfig {
		if err := printResolvedRuntimeFacts(cfg, stdout); err != nil {
			fmt.Fprintf(stderr, "report resolved configuration: %v\n", err)
			return 1
		}
		return 0
	}
	if *temporaryCleanupRequest != "" {
		if dependencies.temporaryLegacyDrainingCleanup == nil {
			fmt.Fprintln(stderr, "temporary legacy Draining cleanup is not assembled")
			return 1
		}
		if err := dependencies.temporaryLegacyDrainingCleanup(
			ctx, cfg, *temporaryCleanupRequest, *temporaryCleanupDigest, stdout,
		); err != nil {
			fmt.Fprintf(stderr, "run temporary legacy Draining cleanup: %v\n", err)
			return 1
		}
		return 0
	}

	recorder := metric.NewRecorder(metric.BuildInfo{
		Version:       version,
		Commit:        commit,
		SchemaVersion: schemaVersion,
	})
	var runErr error
	switch cfg.Input.Mode {
	case config.InputModeGoAccess:
		if dependencies.phaseTwo.run == nil {
			runErr = errPhaseTwoWorkerBundleNotAssembled
		} else {
			runErr = dependencies.phaseTwo.run(ctx, cfg, recorder, dependencies.phaseOne.logger)
		}
	case config.InputModePhaseOneKafkaCompatibility:
		runtimeConfig, err := cfg.PhaseOneCompatibilityRuntimeConfig()
		if err != nil {
			runErr = err
		} else {
			runErr = runApplication(ctx, runtimeConfig, recorder, dependencies.phaseOne)
		}
	default:
		runErr = fmt.Errorf("unsupported input mode %q", cfg.Input.Mode)
	}
	if runErr != nil {
		fmt.Fprintf(stderr, "run alarmd: %v\n", runErr)
		return 1
	}
	return 0
}

func defaultApplicationDependencies(eventLogger *observability.Logger) applicationDependencies {
	return applicationDependencies{
		logger:     eventLogger,
		openBundle: openApplicationBundle,
		newHTTP: func(recorder *metric.Recorder, source observability.HealthSource) (httpRuntime, error) {
			return httpservice.NewWithHealth(recorder, source)
		},
	}
}
