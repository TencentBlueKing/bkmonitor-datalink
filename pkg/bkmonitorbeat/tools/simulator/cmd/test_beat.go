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
	"log"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tools/simulator/internal/events"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tools/simulator/internal/exec"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tools/simulator/internal/server"
)

var execName string

var exceptionInterval time.Duration

var single string

var diskSpacePath string

var diskROPath string

var keywordPath string

var httpPort int

var httpResponse string

var tcpPort int

var tcpResponse string

var udpPort int

var udpResponse string

var processTCPPort int

var processUDPPort int

var promPort int

var rootCmd = &cobra.Command{
	Use:   "test_beat [OPTIONS]",
	Short: "test service for beat",
	Run: func(cmd *cobra.Command, args []string) {
		if execName != "" {
			exec.RunExec(execName, &exec.TestExecConfig{
				PromPort: promPort,
			})
			return
		}
		events.ProduceExceptions(exceptionInterval, single, &events.ExceptConfig{
			DiskSpacePath: diskSpacePath,
			DiskROPath:    diskROPath,
			KeywordPath:   keywordPath,
		})
		server.StartTestServer(exceptionInterval, single, &server.TestServerConfig{
			HTTPPort:       httpPort,
			HTTPResponse:   httpResponse,
			TCPPort:        tcpPort,
			TCPResponse:    tcpResponse,
			UDPPort:        udpPort,
			UDPResponse:    udpResponse,
			ProcessTCPPort: processTCPPort,
			ProcessUDPPort: processUDPPort,
			PromPort:       promPort,
		})
	},
}

func init() {
	rootCmd.PersistentFlags().DurationVar(&exceptionInterval, "exception_interval", time.Minute*10, "interval for exceptions")
	rootCmd.PersistentFlags().StringVar(&execName, "exec", "", "execute once, available: "+strings.Join(exec.AllExecNames, ","))
	names := append([]string{}, events.AllEventNames...)
	names = append(names, server.AllServiceNames...)
	nameString := strings.Join(names, ",")
	rootCmd.PersistentFlags().StringVar(&single, "single", "", "start single exception or service by name, available: "+nameString)
	rootCmd.PersistentFlags().StringVar(&diskSpacePath, "disk_space_path", "/test1", "disk space test path")
	rootCmd.PersistentFlags().StringVar(&diskROPath, "disk_ro_path", "/test2", "disk ro test path")
	rootCmd.PersistentFlags().StringVar(&keywordPath, "keyword_path", "/tmp", "keyword test path")
	rootCmd.PersistentFlags().IntVar(&httpPort, "http_port", 50080, "http listen port")
	rootCmd.PersistentFlags().StringVar(&httpResponse, "http_response", "hello world", "http response")
	rootCmd.PersistentFlags().IntVar(&tcpPort, "tcp_port", 50081, "tcp listen port")
	rootCmd.PersistentFlags().StringVar(&tcpResponse, "tcp_response", "hello world", "tcp response")
	rootCmd.PersistentFlags().IntVar(&udpPort, "udp_port", 50082, "udp listen port")
	rootCmd.PersistentFlags().StringVar(&udpResponse, "udp_response", "hello world", "udp response")
	rootCmd.PersistentFlags().IntVar(&processTCPPort, "process_tcp_port", 50083, "process tcp port")
	rootCmd.PersistentFlags().IntVar(&processUDPPort, "process_udp_port", 50083, "process udp port")
	rootCmd.PersistentFlags().IntVar(&promPort, "prom_port", 50084, "udp listen port")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
