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
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestHelpPrintsUsageAndExitsSuccessfully(t *testing.T) {
	if os.Getenv("BKM_KSM_EXPORTER_HELP_PROCESS") == "1" {
		os.Args = []string{"bkm-ksm-exporter", "-h"}
		main()
		return
	}

	command := exec.Command(os.Args[0], "-test.run=TestHelpPrintsUsageAndExitsSuccessfully")
	command.Env = append(os.Environ(), "BKM_KSM_EXPORTER_HELP_PROCESS=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("bkm-ksm-exporter -h failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Usage of bkm-ksm-exporter:") {
		t.Fatalf("help output missing usage:\n%s", output)
	}
}

func TestParseOptionsPreservesLegacyHPAInvocation(t *testing.T) {
	options, action, err := parseOptions([]string{
		"--listen=:9090",
		"--resync=10m",
		"--sync-timeout=3m",
	})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if action != actionRun {
		t.Fatalf("action=%v, want run", action)
	}
	if options.Collector != collectorHPA {
		t.Fatalf("collector=%q, no-argument legacy mode must remain HPA", options.Collector)
	}
	if options.Listen != ":9090" || options.Resync != 10*time.Minute || options.SyncTimeout != 3*time.Minute {
		t.Fatalf("legacy flags changed: %#v", options)
	}
}

func TestParseOptionsPodTerminatingProfile(t *testing.T) {
	options, action, err := parseOptions([]string{
		"--collector=pod-terminating",
		"--state-namespace=bkmonitor-operator",
		"--state-configmap=pod-terminating-state",
		"--page-limit=2000",
		"--checkpoint-interval=3s",
		"--recovery-hold=10m",
		"--stale-after=15m",
	})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if action != actionRun || options.Collector != collectorPodTerminating {
		t.Fatalf("action=%v collector=%q", action, options.Collector)
	}
	if options.StateNamespace != "bkmonitor-operator" || options.StateConfigMap != "pod-terminating-state" {
		t.Fatalf("state identity mismatch: %#v", options)
	}
	if options.PageLimit != 2000 || options.CheckpointInterval != 3*time.Second {
		t.Fatalf("Pod profile flags mismatch: %#v", options)
	}
}

func TestParseOptionsPreservesVersionEntrypoints(t *testing.T) {
	if _, action, err := parseOptions([]string{"version"}); err != nil || action != actionPrintVersion {
		t.Fatalf("version action=%v err=%v", action, err)
	}
	if _, action, err := parseOptions([]string{"--version"}); err != nil || action != actionPrintBuildInfo {
		t.Fatalf("--version action=%v err=%v", action, err)
	}
}

func TestParseOptionsRejectsUnknownCollector(t *testing.T) {
	if _, _, err := parseOptions([]string{"--collector=unknown"}); err == nil {
		t.Fatal("unknown collector must fail closed")
	}
}

func TestParseOptionsRequiresPodStateIdentityOnlyForPodProfile(t *testing.T) {
	if _, _, err := parseOptions([]string{"--collector=pod-terminating"}); err == nil {
		t.Fatal("Pod profile without state namespace/configmap must fail")
	}
	if _, _, err := parseOptions(nil); err != nil {
		t.Fatalf("legacy HPA profile must not require Pod state flags: %v", err)
	}
}

func TestParseOptionsStateInitProfileUsesSameStateIdentity(t *testing.T) {
	options, action, err := parseOptions([]string{
		"--collector=pod-terminating-state-init",
		"--state-namespace=bkmonitor-operator",
		"--state-configmap=pod-terminating-state",
	})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if action != actionRun || options.Collector != collectorPodTerminatingStateInit {
		t.Fatalf("action=%v collector=%q", action, options.Collector)
	}
}
