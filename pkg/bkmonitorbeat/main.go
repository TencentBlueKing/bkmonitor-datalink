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
	"context"
	"crypto/md5"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	libbeat "github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/cmd/instance"
	"github.com/elastic/beats/libbeat/publisher/processing"
	"github.com/elastic/go-ucfg"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/beater"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs/validator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define/stats"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/report"
	senderagent "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/report/sender/agent"
	senderhttp "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/report/sender/http"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/exceptionbeat/collector"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	BeatName         = "bkmonitorbeat"
	Version          = "unknown"
	reportFlag       = flag.Bool("report", false, "Report event to time series to bkmonitorproxy")
	fakeproc         = flag.String("fakeproc", "", "Show the real pid of the mapping process info")
	disableNormalize = flag.Bool("disable-normalize", false, "If present, disable data normalization")

	verifyRpm             = flag.Bool("verify-rpm", false, "If present, display the rpm packages verify result")
	cgroupBlockWriteBytes = flag.Int("cgroup-block-write-bytes", 0, "set root devices block io write bytes")
	cgroupBlockReadBytes  = flag.Int("cgroup-block-read-bytes", 0, "set root devices block io read bytes")
	cgroupBlockWriteIOps  = flag.Int("cgroup-block-write-iops", 0, "set root devices block io write iops")
	cgroupBlockReadIOps   = flag.Int("cgroup-block-read-iops", 0, "set root devices block io read iops")
)

func registerValidators() {
	err := ucfg.RegisterValidator("regexp", validator.ValidateRegex)
	if err != nil {
		panic(err)
	}
}

func ignoreSignal(c chan os.Signal) {
	s := <-c
	logger.Infof("Got signal:%v", s)
}

func main() {
	senderhttp.Register()

	flag.Parse()
	if *reportFlag {
		senderagent.Register(0)
		if err := report.DoReport(); err != nil {
			fmt.Printf("DoReport failed, err: %+v", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if *fakeproc != "" {
		exe, err := os.Executable()
		bs, err := os.ReadFile(filepath.Join(filepath.Dir(exe), utils.PidStoreFile()))
		if err != nil {
			fmt.Println("failed to read the fakeproc mapping")
			os.Exit(1)
		}

		fmt.Println("Result of the fakeproc:", *fakeproc)
		for _, line := range strings.Split(string(bs), "\n") {
			pair := strings.SplitN(line, " ", 2)
			if len(pair) != 2 {
				continue
			}

			if pair[0] == *fakeproc {
				fmt.Println(pair[1])
			}
		}
		os.Exit(0)
	}

	if *verifyRpm {
		logger.SetOptions(logger.Options{DevNull: true})
		exe, _ := os.Executable()
		h := fmt.Sprintf("%x", md5.Sum([]byte(exe)))

		major, minor, err := utils.GetRootDevices()
		if err != nil {
			fmt.Println("failed to get root devices:", err)
			os.Exit(1)
		}

		err = utils.SetLinuxCGroup(fmt.Sprintf("blockio-%s", h), utils.SpecBlockIO{
			Major:      major,
			Minor:      minor,
			WriteBytes: uint64(*cgroupBlockWriteBytes),
			ReadBytes:  uint64(*cgroupBlockReadBytes),
			WriteIOps:  uint64(*cgroupBlockWriteIOps),
			ReadIOps:   uint64(*cgroupBlockReadIOps),
		})
		if err != nil {
			fmt.Println("failed to set block cgroup:", err)
			os.Exit(1)
		}

		ret, err := utils.RpmVerify(context.Background())
		if err != nil {
			fmt.Println("failed to verify rpm packages:", err)
			os.Exit(1)
		}

		b, _ := json.Marshal(ret)
		fmt.Println(string(b))
		os.Exit(0)
	}

	senderagent.Register(time.Millisecond * 100)
	// add from base reporter
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGPIPE)
	go ignoreSignal(c)

	registerValidators()

	settings := instance.Settings{Processing: processing.MakeDefaultSupport(!*disableNormalize)}
	pubConfig := beat.PublishConfig{PublishMode: libbeat.PublishMode(beat.GuaranteedSend)}

	config, err := beat.InitWithPublishConfig(BeatName, Version, pubConfig, settings)
	if err != nil {
		fmt.Printf("Init filed with error: %v\n", err)
		os.Exit(1)
	}

	// 日志配置
	logCfgContent, err := beat.GetRawConfig().Child("logging", -1)
	if err != nil {
		fmt.Printf("failed to parse logging config: %v\n", err)
		os.Exit(1)
	}

	var logCfg define.LogConfig
	if err := logCfgContent.Unpack(&logCfg); err != nil {
		fmt.Printf("failed to unpack logging config: %v\n", err)
		os.Exit(1)
	}

	define.SetLogConfig(logCfg)
	logger.SetOptions(logger.Options{
		Stdout:     logCfg.Stdout,
		Filename:   filepath.Join(logCfg.Path, "bkmonitorbeat.log"),
		MaxSize:    logCfg.MaxSize,
		MaxAge:     logCfg.MaxAge,
		MaxBackups: logCfg.Backups,
		Level:      logCfg.Level,
	})
	stats.SetVersion(Version)

	bt, err := beater.New(config, BeatName, Version)
	if err != nil {
		fmt.Printf("New failed with error: %v\n", err)
		os.Exit(1)
	}
	collector.Init()
	if err := bt.Run(); err != nil {
		fmt.Printf("failed to run collector: %v\n", err)
		os.Exit(1)
	}
}
