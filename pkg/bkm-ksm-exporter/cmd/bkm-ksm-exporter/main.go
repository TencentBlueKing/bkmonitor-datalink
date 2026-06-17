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
	"flag"
	"log"
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
		listen     string
		kubeconfig string
		resync     time.Duration
		showVer    bool
	)
	flag.StringVar(&listen, "listen", ":8080", "metrics HTTP listen address")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig for out-of-cluster runs; empty uses in-cluster config")
	flag.DurationVar(&resync, "resync", 5*time.Minute, "informer resync period")
	flag.BoolVar(&showVer, "version", false, "print version and exit")
	flag.Parse()

	if showVer {
		log.Printf("bkm-ksm-exporter version=%s buildTime=%s gitHash=%s", version, buildTime, gitHash)
		return
	}

	client, err := kube.NewClient(kubeconfig)
	if err != nil {
		log.Fatalf("build kube client: %v", err)
	}

	stop := make(chan struct{})
	factory := informers.NewSharedInformerFactory(client, resync)
	hpaInformer := factory.Autoscaling().V2().HorizontalPodAutoscalers()
	lister := hpaInformer.Lister()
	_ = hpaInformer.Informer() // ensure the informer is registered before Start

	factory.Start(stop)
	for typ, ok := range factory.WaitForCacheSync(stop) {
		if !ok {
			log.Fatalf("informer cache sync failed: %v", typ)
		}
	}

	srv := exporter.New(listen)
	srv.Register(hpa.New(lister))

	log.Printf("bkm-ksm-exporter %s listening on %s", version, listen)
	if err := srv.Run(); err != nil {
		log.Fatalf("server: %v", err)
	}
}
