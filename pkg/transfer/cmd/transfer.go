// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmd

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

func printLogo() {
	fmt.Printf(define.Logo)
}

func setupPid(pidPath string, force bool) error {
	_, err := os.Stat(pidPath)
	if err == nil {
		if force {
			logging.Warnf("remove pid file %s", pidPath)
			err = os.Remove(pidPath)
			if err != nil {
				logging.Errorf("remove pid file %s error: %v", pidPath, err)
			}
		} else {
			return fmt.Errorf("pid file %s already exists", pidPath)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	return os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644)
}

func tearDownPid(pidPath string) {
	err := os.Remove(pidPath)
	if err != nil {
		logging.Warnf("remove pid file %s error: %v", pidPath, err)
	}
}

func initServiceID(conf define.Configuration) {
	address := conf.GetString(define.ConfHost)
	port := conf.GetInt(define.ConfPort)

	hash := fnv.New32a()
	_, err := hash.Write([]byte(fmt.Sprintf("%s:%d", address, port)))
	if err != nil {
		panic(err)
	}

	define.ServiceID = fmt.Sprintf("%d", hash.Sum32())
}

// transferCmd represents the run command
var transferCmd = &cobra.Command{
	Use:   "run",
	Short: "Run transfer server",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		flags := cmd.Flags()
		pidPath, _ := flags.GetString("pid")
		safety, _ := flags.GetBool("safety")
		err := setupPid(pidPath, !safety)
		if err != nil {
			return err
		}

		return eventbus.Subscribe(eventbus.EvSysFatal, func() {
			tearDownPid(pidPath)
		})
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		flags := cmd.Flags()
		pidPath, _ := flags.GetString("pid")
		tearDownPid(pidPath)
	},
	Run: func(cmd *cobra.Command, args []string) {
		flags := cmd.Flags()
		var err error
		noLogo, _ := flags.GetBool("no-logo")
		if !noLogo {
			printLogo()
		}

		logging.Info("transfer is running")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		conf := config.Configuration
		ctx = config.IntoContext(ctx, conf)

		// 仅在 worker 中才需要初始化 service id
		initServiceID(conf)

		// 仅在运行时，启动redis的缓存维护
		// 同时判断是否有配置停止redis -> transfer,因为有分集群情况下，有集群不需要补cmdb层级信息
		if conf.GetBool(storage.ConfStopCcCache) {
			ctx = context.WithValue(ctx, define.ContextStartCacheKey, false)
		} else {
			ctx = context.WithValue(ctx, define.ContextStartCacheKey, true)
		}

		scheduler, err := define.NewScheduler(ctx, conf.GetString("scheduler.type"))
		logging.PanicIf(err)

		logging.PanicIf(eventbus.SubscribeAsync(eventbus.EvSysExit, func() {
			logging.PanicIf(scheduler.Stop())
		}, false))
		logging.PanicIf(scheduler.Start())
		err = nil
		waitErr := utils.WaitOrTimeOut(conf.GetDuration("scheduler.clean_up_duration"), func() {
			err = scheduler.Wait()
		})
		if waitErr != nil {
			logging.Errorf("transfer clean up timeout: %v", waitErr)
		} else if err != nil {
			logging.Warnf("transfer exit error %v", err)
		} else {
			logging.Infof("transfer exited")
		}
	},
}

func init() {
	rootCmd.AddCommand(transferCmd)
	flags := transferCmd.Flags()
	flags.Bool("no-logo", false, "no logo")
	flags.String("pid", "transfer.pid", "pid file")
	flags.Bool("safety", false, "start transfer safety")
}
