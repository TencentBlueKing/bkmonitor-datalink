// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/viper"

	consulInst "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	consulUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/consul"
)

var (
	ConsulDiffConfigPath string
	Address              string
	Port                 int
	Addr                 string
	SrcPath              string
	DstPath              string
	BypassName           string
)

func InitConfig() error {
	// 存在则以文件中设置为准
	if ConsulDiffConfigPath != "" {
		viper.SetConfigFile(ConsulDiffConfigPath)

		if err := viper.ReadInConfig(); err != nil {
			return fmt.Errorf("read config file: %s error: %s", ConsulDiffConfigPath, err)
		}
		// 赋值
		Address = viper.GetString("consul.address")
		Port = viper.GetInt("consul.port")
		Addr = viper.GetString("consul.addr")
		SrcPath = viper.GetString("consul.srcPath")
		DstPath = viper.GetString("consul.dstPath")
		BypassName = viper.GetString("consul.bypassName")
		return nil
	}
	return nil
}

// GetInstance get consul instance
func GetInstance() *consulInst.Instance {
	// 组装实例
	opt := consulUtils.InstanceOptions{
		Addr:       Address,
		Port:       Port,
		ConsulAddr: Addr,
	}
	client, err := consulInst.NewInstance(context.TODO(), opt)
	if err != nil {
		fmt.Printf("get consul client error, %v", err)
		os.Exit(1)
	}
	return client
}
