package main

import (
	"testing"
	"time"
)

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
