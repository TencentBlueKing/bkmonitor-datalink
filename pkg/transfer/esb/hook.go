// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package esb

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	ConfESBAppCodeKey        = "esb.bk_app_code"
	ConfESBAppSecretKey      = "esb.bk_app_secret"
	ConfESBUserNameKey       = "esb.user_name"
	ConfESBAddress           = "esb.address"
	ConfESBCmdbApiAddress    = "esb.cmdb_apigw_address"
	ConfESBUseAPIGateway     = "esb.use_api_gateway"
	ConfESBBkSupplierAccount = "esb.bk_supplier_account"
	ConfMaxWorker            = "esb.max_worker"              // CMDB同时并发请求客户端个数
	ConfFilterCMDBV3Biz      = "esb.filter_cmdb_v3_location" // 上云版本CMDB会返回CMDBv1及CMDBv2两种版本业务，是否需要过滤仅保留V3的业务
)

func initConfiguration(c define.Configuration) {
	c.SetDefault(ConfESBAppCodeKey, "bkmonitor")
	c.SetDefault(ConfESBAppSecretKey, "")
	c.SetDefault(ConfESBUserNameKey, "admin")
	c.SetDefault(ConfESBAddress, "http://paas.service.consul")
	c.SetDefault(ConfESBCmdbApiAddress, "")
	c.SetDefault(ConfESBUseAPIGateway, false)
	c.SetDefault(ConfESBBkSupplierAccount, "0")
	c.SetDefault(ConfMaxWorker, 2)
	c.SetDefault(ConfFilterCMDBV3Biz, false)
}

func createESBClient(c define.Configuration) {
	ESB = NewClient(c)
	IsFilterCMDBV3Biz = c.GetBool(ConfFilterCMDBV3Biz)
}

func updateTaskManagerConf(c define.Configuration) {
	MaxWorkerConfig = c.GetInt(ConfMaxWorker)
}

func init() {
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPreParse, initConfiguration))
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPostParse, createESBClient))
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPostParse, updateTaskManagerConf))
}
