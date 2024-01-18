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
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	rootCmd.AddCommand(diffCmd)
}

var diffCmd = &cobra.Command{
	Use:   "diff",
	Short: "bk monitor diff",
	Long:  "diff run",
	Run:   startDiff,
}

// start 启动服务
func startDiff(cmd *cobra.Command, args []string) {
	config.InitConfig()
	logger.Infof("start diff consul dataid...")
	if config.BypassSuffixPath == "" {
		logger.Warnf("bypaas suffix path empty, skip compare")
		return
	}
	logger.Infof("bypaas suffix path [%s]", config.BypassSuffixPath)
	db := mysql.GetDBSession().DB
	// 过滤满足条件的记录
	var dataSourceRtList []resulttable.DataSourceResultTable
	if err := resulttable.NewDataSourceResultTableQuerySet(db).Select("bk_data_id").All(&dataSourceRtList); err != nil {
		logger.Errorf("query datasourceresulttable record error, %v", err)
		return
	}
	if len(dataSourceRtList) == 0 {
		logger.Infof("no data source result table records")
		return
	}
	var dataIdList []uint
	for _, dsrt := range dataSourceRtList {
		dataIdList = append(dataIdList, dsrt.BkDataId)
	}
	dataIdList = slicex.RemoveDuplicate(&dataIdList)

	var dataSourceList []resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(db).IsEnableEq(true).
		BkDataIdIn(dataIdList...).OrderDescByLastModifyTime().All(&dataSourceList); err != nil {
		logger.Errorf("query datasource record error, %v", err)
		return
	}

	c, err := consul.GetInstance()
	if err != nil {
		logger.Errorf("get consul client failed, %v", err)
		return
	}
	for _, ds := range dataSourceList {
		svc := service.NewDataSourceSvc(&ds)
		path := svc.ConsulConfigPath()
		realPath := strings.Replace(path, config.BypassSuffixPath, "", 1)
		dataBytes, err := c.Get(path)
		if err != nil {
			logger.Errorf("data_id [%v] get data from [%s] failed", ds.BkDataId, path)
			continue
		}
		realDataBytes, err := c.Get(realPath)
		if err != nil {
			logger.Errorf("data_id [%v] get data from [%s] failed", ds.BkDataId, realPath)
			continue
		}
		if len(dataBytes) == 0 && len(realDataBytes) == 0 {
			continue
		}
		dataJson := string(dataBytes)
		realDataJson := string(realDataBytes)
		// 	去除时间字段，避免影响比对
		re := regexp.MustCompile(`"create_time":\s?(?P<datetime>\d+),`)
		matchedList := re.FindAllStringSubmatch(dataJson, -1)
		matchedList2 := re.FindAllStringSubmatch(realDataJson, -1)
		for _, s := range matchedList {
			dataJson = strings.ReplaceAll(dataJson, s[1], "0")
		}
		for _, s := range matchedList2 {
			realDataJson = strings.ReplaceAll(realDataJson, s[1], "0")
		}
		equal, err := jsonx.CompareJson(dataJson, realDataJson)
		if err != nil {
			logger.Errorf("data_id [%v] compare json [%s] and [%s] failed, %v", ds.BkDataId, dataJson, realDataJson, err)
			continue
		}
		if equal {
			logger.Infof("data_id [%v] consul data is equal", ds.BkDataId)
		} else {
			logger.Warnf("data_id [%v] consul data is different", ds.BkDataId)
			logger.Warnf("data_id [%v] new data [%s]", ds.BkDataId, dataJson)
			logger.Warnf("data_id [%v] old data [%s]", ds.BkDataId, realDataJson)
		}
	}
}
