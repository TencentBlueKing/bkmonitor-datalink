// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

// Command bkm-ksm-exporter hosts compatibility collectors that are deployed as
// separate profiles. Its no-argument behavior remains the legacy HPA exporter.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkm-ksm-exporter/collectors/hpa"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkm-ksm-exporter/collectors/podterminating"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkm-ksm-exporter/exporter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkm-ksm-exporter/internal/kube"
)

var (
	version   = "dev"
	buildTime = "unknown"
	gitHash   = "unknown"
)

const (
	collectorHPA                     = "hpa"
	collectorPodTerminating          = "pod-terminating"
	collectorPodTerminatingStateInit = "pod-terminating-state-init"
)

type commandAction int

const (
	actionRun commandAction = iota
	actionPrintVersion
	actionPrintBuildInfo
)

type options struct {
	Collector  string
	Listen     string
	Kubeconfig string

	Resync      time.Duration
	SyncTimeout time.Duration

	StateNamespace     string
	StateConfigMap     string
	PageLimit          int64
	RequestTimeout     time.Duration
	CheckpointInterval time.Duration
	RecoveryHold       time.Duration
	StaleAfter         time.Duration
	ClientQPS          float64
	ClientBurst        int
}

func parseOptions(arguments []string) (options, commandAction, error) {
	var parsed options
	var showVersion bool
	flags := flag.NewFlagSet("bkm-ksm-exporter", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&parsed.Collector, "collector", collectorHPA, "collector profile: hpa, pod-terminating, or pod-terminating-state-init")
	flags.StringVar(&parsed.Listen, "listen", ":8080", "metrics HTTP listen address")
	flags.StringVar(&parsed.Kubeconfig, "kubeconfig", "", "kubeconfig for out-of-cluster runs; empty uses in-cluster config")
	flags.DurationVar(&parsed.Resync, "resync", 5*time.Minute, "HPA informer resync period")
	flags.DurationVar(&parsed.SyncTimeout, "sync-timeout", 2*time.Minute, "HPA initial informer sync timeout")
	flags.StringVar(&parsed.StateNamespace, "state-namespace", "", "Pod profile state ConfigMap namespace")
	flags.StringVar(&parsed.StateConfigMap, "state-configmap", "", "Pod profile state ConfigMap name")
	flags.Int64Var(&parsed.PageLimit, "page-limit", 1_000, "Pod snapshot List page size")
	flags.DurationVar(&parsed.RequestTimeout, "request-timeout", 30*time.Second, "timeout for each Kubernetes List/Get/Patch request")
	flags.DurationVar(&parsed.CheckpointInterval, "checkpoint-interval", 5*time.Second, "minimum interval for coalesced Pod state persistence")
	flags.DurationVar(&parsed.RecoveryHold, "recovery-hold", 10*time.Minute, "same-dimension zero-value recovery retention")
	flags.DurationVar(&parsed.StaleAfter, "stale-after", 15*time.Minute, "suppress business rows after persistence remains unhealthy")
	flags.Float64Var(&parsed.ClientQPS, "client-qps", 20, "Pod profile Kubernetes client QPS")
	flags.IntVar(&parsed.ClientBurst, "client-burst", 40, "Pod profile Kubernetes client burst")
	flags.BoolVar(&showVersion, "version", false, "print build information and exit")
	if err := flags.Parse(arguments); err != nil {
		return options{}, actionRun, err
	}
	if flags.NArg() > 0 {
		if flags.NArg() == 1 && flags.Arg(0) == "version" {
			return parsed, actionPrintVersion, nil
		}
		return options{}, actionRun, fmt.Errorf("unexpected positional arguments %q", flags.Args())
	}
	if showVersion {
		return parsed, actionPrintBuildInfo, nil
	}
	switch parsed.Collector {
	case collectorHPA:
		if parsed.Resync <= 0 || parsed.SyncTimeout <= 0 {
			return options{}, actionRun, fmt.Errorf("HPA resync and sync-timeout must be positive")
		}
	case collectorPodTerminating, collectorPodTerminatingStateInit:
		switch {
		case parsed.StateNamespace == "":
			return options{}, actionRun, fmt.Errorf("--state-namespace is required for the pod-terminating collector")
		case parsed.StateConfigMap == "":
			return options{}, actionRun, fmt.Errorf("--state-configmap is required for the pod-terminating collector")
		case parsed.PageLimit <= 0:
			return options{}, actionRun, fmt.Errorf("--page-limit must be positive")
		case parsed.RequestTimeout <= 0:
			return options{}, actionRun, fmt.Errorf("--request-timeout must be positive")
		case parsed.CheckpointInterval <= 0:
			return options{}, actionRun, fmt.Errorf("--checkpoint-interval must be positive")
		case parsed.RecoveryHold <= 0:
			return options{}, actionRun, fmt.Errorf("--recovery-hold must be positive")
		case parsed.StaleAfter < parsed.RecoveryHold:
			return options{}, actionRun, fmt.Errorf("--stale-after must be greater than or equal to --recovery-hold")
		case parsed.ClientQPS <= 0:
			return options{}, actionRun, fmt.Errorf("--client-qps must be positive")
		case parsed.ClientBurst <= 0:
			return options{}, actionRun, fmt.Errorf("--client-burst must be positive")
		}
	default:
		return options{}, actionRun, fmt.Errorf("unsupported collector %q", parsed.Collector)
	}
	return parsed, actionRun, nil
}

func main() {
	parsed, action, err := parseOptions(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Fatal(err)
	}
	switch action {
	case actionPrintVersion:
		fmt.Println(version)
		return
	case actionPrintBuildInfo:
		log.Printf("bkm-ksm-exporter version=%s buildTime=%s gitHash=%s", version, buildTime, gitHash)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, parsed); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, parsed options) error {
	switch parsed.Collector {
	case collectorHPA:
		client, err := kube.NewClient(parsed.Kubeconfig)
		if err != nil {
			return fmt.Errorf("build Kubernetes client: %w", err)
		}
		return runHPA(ctx, client, parsed)
	case collectorPodTerminating:
		client, err := kube.NewClientWithRateLimit(parsed.Kubeconfig, float32(parsed.ClientQPS), parsed.ClientBurst)
		if err != nil {
			return fmt.Errorf("build Kubernetes client: %w", err)
		}
		return runPodTerminating(ctx, client, parsed)
	case collectorPodTerminatingStateInit:
		client, err := kube.NewClient(parsed.Kubeconfig)
		if err != nil {
			return fmt.Errorf("build Kubernetes client: %w", err)
		}
		return podterminating.EnsureStateConfigMap(
			ctx,
			client.CoreV1().ConfigMaps(parsed.StateNamespace),
			parsed.StateConfigMap,
			parsed.RequestTimeout,
			podterminating.HardMaxStateBytes,
		)
	default:
		return fmt.Errorf("unsupported collector %q", parsed.Collector)
	}
}

func runHPA(ctx context.Context, client kubernetes.Interface, parsed options) error {
	factory := informers.NewSharedInformerFactory(client, parsed.Resync)
	hpaInformer := factory.Autoscaling().V2().HorizontalPodAutoscalers()
	lister := hpaInformer.Lister()
	_ = hpaInformer.Informer()

	server := exporter.New(parsed.Listen)
	server.Register(hpa.New(lister))
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Run(runContext)
	}()

	discoveryResult := make(chan error, 1)
	go func() {
		_, err := client.Discovery().ServerResourcesForGroupVersion("autoscaling/v2")
		discoveryResult <- err
	}()
	discoveryTimer := time.NewTimer(parsed.SyncTimeout)
	defer discoveryTimer.Stop()
	select {
	case <-ctx.Done():
		return nil
	case err := <-serverErrors:
		return err
	case <-discoveryTimer.C:
		return fmt.Errorf("autoscaling/v2 API discovery timed out after %s", parsed.SyncTimeout)
	case err := <-discoveryResult:
		if err != nil {
			return fmt.Errorf("autoscaling/v2 API discovery failed: %w", err)
		}
	}

	factory.Start(runContext.Done())
	syncContext, syncCancel := context.WithTimeout(runContext, parsed.SyncTimeout)
	defer syncCancel()
	for resourceType, ok := range factory.WaitForCacheSync(syncContext.Done()) {
		if !ok {
			return fmt.Errorf("HPA informer cache sync timed out or failed for %v", resourceType)
		}
	}
	server.SetReady(true)
	log.Printf("bkm-ksm-exporter %s HPA cache synced, serving on %s", version, parsed.Listen)

	select {
	case <-ctx.Done():
		return nil
	case err := <-serverErrors:
		return err
	}
}

func runPodTerminating(ctx context.Context, client kubernetes.Interface, parsed options) error {
	store, err := podterminating.NewStateStore(
		client.CoreV1().ConfigMaps(parsed.StateNamespace),
		parsed.StateConfigMap,
		parsed.RequestTimeout,
		podterminating.HardMaxStateBytes,
	)
	if err != nil {
		return err
	}
	state := podterminating.NewState(parsed.RecoveryHold, parsed.StaleAfter)
	server := exporter.New(parsed.Listen)
	server.Register(podterminating.NewCollector(state, time.Now))
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	errorsChannel := make(chan error, 2)
	go func() {
		errorsChannel <- server.Run(runContext)
	}()

	persisted, stateBytes, err := store.Load(runContext)
	if err != nil {
		return err
	}
	if err := state.Restore(persisted, stateBytes, time.Now()); err != nil {
		return fmt.Errorf("restore Pod terminating state: %w", err)
	}
	runner, err := podterminating.NewRunner(
		client.CoreV1().Pods(""),
		state,
		store,
		podterminating.RunnerOptions{
			PageLimit:          parsed.PageLimit,
			RequestTimeout:     parsed.RequestTimeout,
			CheckpointInterval: parsed.CheckpointInterval,
			SetReady:           server.SetReady,
		},
	)
	if err != nil {
		return err
	}

	go func() {
		errorsChannel <- runner.Run(runContext)
	}()
	log.Printf("bkm-ksm-exporter %s Pod terminating profile starting on %s", version, parsed.Listen)

	select {
	case <-ctx.Done():
		return nil
	case err := <-errorsChannel:
		return err
	}
}
