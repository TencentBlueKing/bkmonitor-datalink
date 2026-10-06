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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// repairOpenSegmentsCommand is the name that selects the repair.
const repairOpenSegmentsCommand = "repair-open-segments"

// runRepairOpenSegments puts open Segments back on the object their publication
// names.
//
// A one-off operator action, run by exec into a leader pod with that pod's own
// configuration, which is why it takes a config path and no connection flags:
// the credentials are already in the file this process reads, and a command
// that accepts them on a command line puts them in a shell history and a
// process list.
//
// It is a separate subcommand rather than a start-up step because it is not
// part of running: it repairs a state that the code can no longer produce, and
// a repair that runs by itself is one nobody decides to run.
func runRepairOpenSegments(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("alarmd "+repairOpenSegmentsCommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to alarmd YAML configuration")
	apply := flags.Bool("apply", false,
		"write the repairs; without it the command only reports what it would write")
	evidenceDir := flags.String("evidence-dir", "",
		"directory to write the stored bytes of every changed key, before and after; required with --apply")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %v\n", flags.Args())
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "--config is required")
		return 2
	}
	if *apply && *evidenceDir == "" {
		fmt.Fprintln(stderr, "--apply requires --evidence-dir: a repair nobody can read back afterwards "+
			"is a change nobody can check or undo")
		return 2
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return 1
	}
	client, err := openProductionRedisWithHook(ctx, cfg.RuntimeStoreRedis(), nil)
	if err != nil {
		fmt.Fprintf(stderr, "connect to the runtime store: %v\n", err)
		return 1
	}
	defer func() { _ = client.Close() }()
	repository, err := controlplane.NewRedisCatalogRepository(
		client, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog"), phaseTwoCatalogRetention(cfg),
	)
	if err != nil {
		fmt.Fprintf(stderr, "open the catalog repository: %v\n", err)
		return 1
	}

	report, err := controlplane.RepairOpenSegments(ctx, repository, controlplane.RepairOpenSegmentsOptions{
		Apply: *apply, EvidenceDir: *evidenceDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "repair open segments: %v\n", err)
		return 1
	}
	printOpenSegmentRepairReport(stdout, report, *apply)
	if report.Conflicts > 0 {
		// Another writer moved a timeline between the read and the write. Not
		// an error in what was written -- nothing was, for those -- but the run
		// is incomplete and saying so is the whole point.
		return 1
	}
	return 0
}

func printOpenSegmentRepairReport(stdout io.Writer, report controlplane.OpenSegmentRepairReport, applied bool) {
	fmt.Fprintf(stdout, "publication %s, %d schedule timelines scanned\n", report.Revision, report.Scanned)
	for _, repair := range report.Repairs {
		fmt.Fprintf(stdout,
			"%s open_digest=%s manifest_digest=%s refs_match=%t record_revision=%d applied=%t\n",
			repair.QueryGroup, repair.OpenDigest, repair.ManifestDigest,
			repair.RefsMatch, repair.RecordRevision, repair.Applied)
	}
	verb := "would repair"
	if applied {
		verb = "repaired"
	}
	fmt.Fprintf(stdout, "%s %d open Segments", verb, len(report.Repairs))
	if report.Conflicts > 0 {
		fmt.Fprintf(stdout, ", %d lost a compare-and-set and were not written", report.Conflicts)
	}
	fmt.Fprintln(stdout)
}
