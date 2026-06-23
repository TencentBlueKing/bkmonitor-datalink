// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Command bkm-ksm-exporter exposes kube_hpa_* metrics (read from autoscaling/v2)
// for clusters where the bundled kube-state-metrics v1.9.7 cannot, because it
// reads the removed autoscaling/v2beta1 API.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/informers"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkm-ksm-exporter/collectors/hpa"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkm-ksm-exporter/exporter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkm-ksm-exporter/internal/kube"
)

// Injected at build time via -ldflags.
var (
	version   = "dev"
	buildTime = "unknown"
	gitHash   = "unknown"
)

func main() {
	var (
		listen      string
		kubeconfig  string
		resync      time.Duration
		syncTimeout time.Duration
		showVer     bool
	)
	flag.StringVar(&listen, "listen", ":8080", "metrics HTTP listen address")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig for out-of-cluster runs; empty uses in-cluster config")
	flag.DurationVar(&resync, "resync", 5*time.Minute, "informer resync period")
	flag.DurationVar(&syncTimeout, "sync-timeout", 2*time.Minute, "max wait for the initial informer cache sync before exiting for restart")
	flag.BoolVar(&showVer, "version", false, "print version and exit")
	flag.Parse()

	// The `version` subcommand prints only the version string: the image-build
	// pipeline runs `bkm-ksm-exporter version` and uses its output as the image
	// tag. The -version flag prints the fuller build info for humans.
	if flag.Arg(0) == "version" {
		fmt.Println(version)
		return
	}
	if showVer {
		log.Printf("bkm-ksm-exporter version=%s buildTime=%s gitHash=%s", version, buildTime, gitHash)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := kube.NewClient(kubeconfig)
	if err != nil {
		log.Fatalf("build kube client: %v", err)
	}

	factory := informers.NewSharedInformerFactory(client, resync)
	hpaInformer := factory.Autoscaling().V2().HorizontalPodAutoscalers()
	lister := hpaInformer.Lister()
	_ = hpaInformer.Informer() // ensure the informer is registered before Start

	srv := exporter.New(listen)
	srv.Register(hpa.New(lister))

	// Serve /healthz (liveness) and /metrics immediately, before the cache sync.
	// A slow or permanently failing sync must not let the liveness probe kill the
	// pod before we exit deliberately below.
	go func() {
		if err := srv.Run(ctx); err != nil {
			log.Fatalf("server: %v", err)
		}
	}()

	factory.Start(ctx.Done())

	// Bounded wait for the initial sync. If autoscaling/v2 LIST keeps failing
	// (RBAC denied, apiserver 5xx) the informer never syncs -- do NOT block here
	// forever. Time out and exit non-zero so Kubernetes restarts us
	// (CrashLoopBackOff), which is far easier to detect than a process stuck
	// silently before it ever serves metrics.
	syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	for typ, ok := range factory.WaitForCacheSync(syncCtx.Done()) {
		if !ok {
			log.Fatalf("informer cache sync timed out or failed (api unavailable / RBAC?) for %v", typ)
		}
	}
	log.Printf("bkm-ksm-exporter %s cache synced, serving on %s", version, listen)

	<-ctx.Done()
	log.Printf("bkm-ksm-exporter shutting down")
}
