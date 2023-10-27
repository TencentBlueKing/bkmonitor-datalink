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
	"math/rand"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/cstockton/go-conv"
	_ "go.uber.org/automaxprocs"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/cmd"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

func handleSignal() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		dying := false
		for sig := range signals {
			logging.Infof("signal %v received", sig)
			switch sig {
			case syscall.SIGINT, syscall.SIGTERM:
				if dying {
					eventbus.Publish(eventbus.EvSysKill)
					logging.Errorf("program kill by signal %v", sig)
				} else {
					dying = true
					eventbus.Publish(eventbus.EvSysExit)
				}
			case syscall.SIGHUP:
				eventbus.Publish(eventbus.EvSysUpdate)
			}
		}
	}()
}

var preRunOnce, postRunOnce sync.Once

func preRun() {
	preRunOnce.Do(func() {
		rand.Seed(time.Now().Unix())

		utils.CheckError(eventbus.SubscribeAsync(eventbus.EvSysUpdate, func() {
			eventbus.Publish(eventbus.EvSigUpdateCCCache, make(map[string]string))
		}, false))

		utils.CheckError(eventbus.SubscribeAsync(eventbus.EvSigDumpStack, func(params map[string]string) {
			logging.Goroutines()
		}, false))
		utils.CheckError(eventbus.Subscribe(eventbus.EvSysFatal, logging.Goroutines))

		utils.CheckError(eventbus.Subscribe(eventbus.EvSigSetBlockProfile, func(params map[string]string) {
			value := 0
			rate, ok := params["rate"]
			if ok {
				value = conv.Int(rate)
			}

			runtime.SetBlockProfileRate(value)

			logging.Infof("block profile rate set to %d", value)
		}))

		handleSignal()
		eventbus.Publish(eventbus.EvSysPreRun)
	})
}

func postRun() {
	postRunOnce.Do(func() {
		eventbus.Publish(eventbus.EvSysPostRun)
		eventbus.Global.WaitAsync()
	})
}

func main() {
	defer utils.RecoverError(func(e error) {
		logging.Fatalf("system panic: %+v", e)
	})

	preRun()
	cmd.Execute()
	postRun()
}
