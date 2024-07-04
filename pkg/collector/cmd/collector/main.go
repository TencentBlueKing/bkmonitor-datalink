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
	"fmt"
	"os"
	"time"

	libbeat "github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/cmd/instance"
	"github.com/elastic/beats/libbeat/publisher/processing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/controller"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
)

var (
	appName   = "bk-collector"
	version   = "unknown.version"
	gitHash   = "unknown.gitHash"
	buildTime = "unknown.buildTime"
)

func main() {
	settings := instance.Settings{Processing: processing.MakeDefaultSupport(false)}
	pubConfig := beat.PublishConfig{PublishMode: libbeat.PublishMode(beat.GuaranteedSend)}

	config, err := beat.InitWithPublishConfig(appName, version, pubConfig, settings)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to init beat config: %v\n", err)
		os.Exit(1)
	}

	collector, err := controller.New(confengine.New(config), define.BuildInfo{
		Version: version,
		GitHash: gitHash,
		Time:    buildTime,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create new beat: %v\n", err)
		os.Exit(1)
	}

	if err := collector.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start collector %v\n", err)
		os.Exit(1)
	}
	defer utils.HandleCrash()

	// 收敛 reload 行为 避免频繁 reload 导致 CPU 拉高
	duration := time.Second * 10
	timer := time.NewTimer(duration)
	timer.Stop()

	for {
		select {
		case <-beat.ReloadChan: // 重载信号
			timer.Reset(duration)

		case <-timer.C:
			if conf := beat.GetConfig(); conf != nil {
				if err := collector.Reload(confengine.New(conf)); err != nil {
					fmt.Fprintln(os.Stderr, "failed to reload controller")
				}
			}

		case <-beat.Done: // 结束信号
			if err := collector.Stop(); err != nil {
				fmt.Fprintln(os.Stderr, "failed to stop controller")
			}
			return
		}
	}
}
