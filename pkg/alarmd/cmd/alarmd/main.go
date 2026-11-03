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
	"strings"
	"sync"
	"syscall"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/clirelease"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

var (
	version       = "dev"
	commit        = "unknown"
	schemaVersion = "none"
)

// embeddedCLIRelease is the alarmd-cli release this binary carries, read
// once: the build stamps version, and the release must name the same one.
var embeddedCLIRelease = sync.OnceValue(func() *clirelease.Release { return clirelease.Load(version) })

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

// checkConfigUnknownKeys is --check-config's exit code for a file that
// loads but carries keys nothing reads: the facts are printed, the keys are
// named on stderr, and the preflight does not pass.
const checkConfigUnknownKeys = 3

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	eventLogger := observability.New(observability.ComponentTrigger, stderr)
	return runWithRuntimeModeDependencies(ctx, args, stdout, stderr, runtimeModeDependencies{
		logger:   eventLogger,
		phaseTwo: defaultPhaseTwoApplicationDependencies(),
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
	checkConfig := flags.Bool("check-config", false, "validate configuration and exit; credential files are not opened, so the shape of "+
		"a credential given by file (the CLI admin key's length and characters, a Redis username's whitespace) is checked only with --resolve-credentials")
	resolveCredentials := flags.Bool("resolve-credentials", false, "with --check-config, also read every credential file the configuration names, "+
		"for every role, and check what each holds")
	showVersion := flags.Bool("version", false, "print build information and exit")
	checkCLIRelease := flags.Bool("check-cli-release", false, "verify the embedded alarmd-cli release against this binary and exit")
	selectedRoles := flags.String("roles", "", "comma-separated process roles (default: control,worker,channel)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %v\n", flags.Args())
		return 2
	}
	terminalModes := 0
	for _, enabled := range []bool{*showVersion, *checkConfig, *checkCLIRelease} {
		if enabled {
			terminalModes++
		}
	}
	if terminalModes > 1 {
		fmt.Fprintln(stderr, "--version, --check-config and --check-cli-release cannot be used together")
		return 2
	}
	if *resolveCredentials && !*checkConfig {
		fmt.Fprintln(stderr, "--resolve-credentials is used with --check-config")
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "alarmd version=%s commit=%s schema_version=%s\n", version, commit, schemaVersion)
		return 0
	}
	if *checkCLIRelease {
		// The build's own check: every digest recomputed, the release named
		// for this binary's version. Serving trusts that it passed.
		release := embeddedCLIRelease()
		if err := release.Verify(); err != nil {
			fmt.Fprintf(stderr, "alarmd-cli release: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "alarmd-cli release version=%s targets=%s\n", release.Version(), strings.Join(release.TargetNames(), ","))
		return 0
	}

	// Resolve the CPU quota before the configuration is read: the capacity
	// budgets are derived from the container's CPU budget, and reading them
	// off an unadjusted GOMAXPROCS would size the process for the host.
	if dependencies.phaseTwo.configureCPU != nil {
		if _, err := dependencies.phaseTwo.configureCPU(); err != nil {
			fmt.Fprintf(stderr, "configure CPU budget: %v\n", err)
			return 1
		}
	}

	var override roles.Set
	rolesSpecified := false
	flags.Visit(func(f *flag.Flag) { rolesSpecified = rolesSpecified || f.Name == "roles" })
	if rolesSpecified {
		var err error
		override, err = roles.Parse(*selectedRoles)
		if err != nil {
			fmt.Fprintf(stderr, "resolve roles: %v\n", err)
			return 2
		}
	}
	// A process reads the credential files its roles use. --check-config
	// opens none: release checks run it on the final YAML where no Secret
	// is mounted, so it checks that each reference is a plain absolute path
	// and that each credential has one source, but cannot check what a file
	// holds (the admin key's length and characters, a username's
	// whitespace). --resolve-credentials reads them all, for checking where
	// the files are in place. No credential is printed in any mode.
	credentials := config.CredentialsForRoles
	if *checkConfig {
		credentials = config.CredentialsUnread
		if *resolveCredentials {
			credentials = config.CredentialsAll
		}
	}
	cfg, err := config.LoadWithCredentials(*configPath, override, credentials)
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return 1
	}
	for _, key := range cfg.UnknownKeys {
		fmt.Fprintf(stderr, "load configuration: ignored a key nothing reads: %s\n", key)
	}
	if *checkConfig {
		if err := printResolvedRuntimeFacts(cfg, stdout); err != nil {
			fmt.Fprintf(stderr, "report resolved configuration: %v\n", err)
			return 1
		}
		// A process starts past a key nothing reads; a preflight does not
		// pass one. A misspelled live key (roles:, say) falls back to its
		// default, which can be broader than what was meant, and the
		// preflight is where that is caught.
		if len(cfg.UnknownKeys) > 0 {
			return checkConfigUnknownKeys
		}
		return 0
	}
	recorder := metric.NewRecorder(metric.BuildInfo{
		Version:       version,
		Commit:        commit,
		SchemaVersion: schemaVersion,
	})
	recorder.SetConfigUnknownKeys(len(cfg.UnknownKeys))
	var runErr error
	if dependencies.phaseTwo.run == nil {
		runErr = errPhaseTwoWorkerBundleNotAssembled
	} else {
		runErr = dependencies.phaseTwo.run(ctx, cfg, recorder, dependencies.logger)
	}
	if runErr != nil {
		fmt.Fprintf(stderr, "run alarmd: %v\n", runErr)
		return 1
	}
	return 0
}
