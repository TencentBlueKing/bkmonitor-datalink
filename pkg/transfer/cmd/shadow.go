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
	"os"
	"path"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// shadowCmd represents the shadow command
var shadowCmd = &cobra.Command{
	Use:   "shadow",
	Short: "Print shadow info",
	Run: func(cmd *cobra.Command, args []string) {
		flags := cmd.Flags()

		serviceFiltering, err := flags.GetString("service")
		checkError(err, -1, "get service failed")
		sourceFiltering, err := flags.GetString("source")
		checkError(err, -1, "get source failed")
		targetFiltering, err := flags.GetString("target")
		checkError(err, -1, "get target failed")

		helper, err := scheduler.NewClusterHelper(context.Background(), config.Configuration)
		checkError(err, -1, "cluster config failed")

		clusterInfo, err := helper.ListAllLeaders()
		checkError(err, -1, "get cluster service information failed")

		// 拼接所有的cluster
		conf := config.Configuration
		pathVersion := conf.GetString(consul.ConfKeyPathVersion)
		dataIDPath, shadowIDPath := parsePathByPathVersion(pathVersion, conf)

		table := tablewriter.NewWriter(os.Stdout)
		tableHeader := parseTableByPathVersion(pathVersion, []string{"cluster", "service", "source", "target"})
		table.SetHeader(tableHeader)
		counter := utils.NewCounter(utils.StringCounterComparator)

		for _, cluster := range clusterInfo {
			var dataIDRoot, shadowIDRoot string
			var dispatcher *consul.Dispatcher
			var clusterName string

			switch pathVersion {
			case "":
				dispatcher = helper.Dispatcher
			default:
				clusterName = cluster.Meta["cluster_id"]
				serviceName := cluster.Meta["service"]
				dataIDRoot = path.Join(dataIDPath, clusterName, "data_id")
				shadowIDRoot = path.Join(shadowIDPath, clusterName, "data_id")

				dispatcher = consul.NewDispatcher(consul.DispatcherConfig{
					Context:         helper.Context,
					Converter:       scheduler.NewDispatchConverter(dataIDRoot, shadowIDRoot),
					Client:          helper.Client,
					TargetRoot:      shadowIDRoot,
					ManualRoot:      helper.ManualRoot,
					TriggerCreator:  consul.NewServiceTriggerCreator(helper.Client, dataIDRoot, serviceName, clusterName+"-"+cluster.Tags[0]),
					DispatchDelay:   helper.Configuration.GetDuration(consul.ConfKeyDispatchDelay),
					RecoverInterval: helper.Configuration.GetDuration(consul.ConfKeyDispatchInterval),
				})
			}

			var sourcePrefix string
			if showPrefix, err := flags.GetBool("show-prefix"); err == nil && showPrefix {
				sourcePrefix, err := flags.GetString("source-prefix")
				checkError(err, -1, "get source-prefix failed")
				if sourcePrefix == "" {
					sourcePrefix = helper.DataIDRoot + "/"
				}
			}

			checkError(dispatcher.Recover(), -3, "recover shadows failed")

			dispatcher.VisitPlan(func(service *define.ServiceDispatchInfo, pair *define.PairDispatchInfo) bool {
				if !strings.Contains(service.Service, serviceFiltering) ||
					!strings.Contains(pair.Source, sourceFiltering) ||
					!strings.Contains(pair.Target, targetFiltering) {
					return true
				}

				counter.Incr(service.Service)
				tableRow := []string{clusterName, service.Service, strings.TrimPrefix(pair.Source, sourcePrefix), pair.Target}
				table.Append(parseTableByPathVersion(pathVersion, tableRow))
				return true
			})
		}

		// Naïve algorithm
		n := 0
		sum := 0
		sumSq := 0
		counter.Visit(func(item interface{}, value int) {
			n++
			sum += value
			sumSq += value * value
		})
		avg := float64(sum) / float64(n)

		variance := 0.0
		if n > 1 {
			variance = (float64(sumSq) - float64(sum*sum)/float64(n)) / float64(n-1)
		}

		table.SetCaption(true, fmt.Sprintf("%d links, avg:%.2f, var:%.2f \n", sum, avg, variance))

		table.Render()
	},
}

// 根据不同的路径规则 处理获取data_id_path和shadow_path
func parsePathByPathVersion(pathVersion string, conf define.Configuration) (dataIDPath, shadowIDPath string) {
	dataIDPath = conf.GetString(consul.ConfKeyDataIDPath)
	servicePath := conf.GetString(consul.ConfKeyServicePath)
	shadowIDPath = path.Join(servicePath, "data_id")
	switch pathVersion {
	case "":
		return dataIDPath, shadowIDPath
	default:
		// xxx/v1/cluster/data_id => xxx/v1/
		dataIDClusterPath, _ := path.Split(strings.Trim(dataIDPath, "/"))
		dataIDPath, _ = path.Split(strings.Trim(dataIDClusterPath, "/"))

		// xxx/service/v1/cluster => xxx/service/v1/
		shadowIDPath, _ = path.Split(strings.Trim(servicePath, "/"))
		return dataIDPath, shadowIDPath
	}
}

func init() {
	rootCmd.AddCommand(shadowCmd)
	flags := shadowCmd.Flags()
	flags.StringP("service", "i", "", "service filtering")
	flags.StringP("source", "s", "", "source filtering")
	flags.StringP("target", "t", "", "target filtering")
	flags.StringP("source-prefix", "S", "", "source prefix")
	flags.BoolP("show-prefix", "P", false, "print source prefix")
}
