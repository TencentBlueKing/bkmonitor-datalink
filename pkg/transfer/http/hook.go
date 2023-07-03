// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"crypto/md5"
	"fmt"
	"net"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	ConfAutoShutdown   = "http.auto_shutdown"
	ConfAuthToken      = "http.auth.token"
	ConfAuthExemptPath = "http.auth.exempt_path"
)

func setDefaultAuthToken() string {
	user := "transfer"
	password := md5.New()
	password.Write([]byte(user))

	interfaces, err := net.Interfaces()
	if err != nil {
		for _, i := range interfaces {
			password.Write([]byte(i.HardwareAddr))
		}
	}

	return fmt.Sprintf("%s:%x", user, password.Sum(nil))
}

func initConfiguration(c define.Configuration) {
	c.SetDefault(ConfAutoShutdown, false)
	c.SetDefault(ConfAuthToken, setDefaultAuthToken())
	c.SetDefault(ConfAuthExemptPath, []string{
		"/metrics",
		"/cache",
	})
}

func init() {
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPreParse, initConfiguration))
}
