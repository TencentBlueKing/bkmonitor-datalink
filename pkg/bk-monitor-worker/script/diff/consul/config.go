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

	"github.com/pkg/errors"
	"github.com/spf13/viper"

	consulInst "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	consulUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/consul"
)

// 源配置
type SrcConsulConfig struct {
	Address string `mapstructure:"address"`
	Port    int    `mapstructure:"port"`
	Path    string `mapstructure:"path"`
}

// 旁路配置
type BypassConsulConfig struct {
	Address string `mapstructure:"address"`
	Port    int    `mapstructure:"port"`
	Path    string `mapstructure:"path"`
}

type ConsulConfig struct {
	Src    SrcConsulConfig    `mapstructure:"srcConsul"`
	Bypass BypassConsulConfig `mapstructure:"bypassConsul"`
}

// 指定配置文件的路径
var (
	ConsulDiffConfigPath string
	Config               = &ConsulConfig{}
)

func InitConfig() error {
	// 存在则以文件中设置为准
	fmt.Println(ConsulDiffConfigPath)
	if ConsulDiffConfigPath != "" {
		viper.SetConfigFile(ConsulDiffConfigPath)

		if err := viper.ReadInConfig(); err != nil {
			return errors.Errorf("read config file: %s error: %s", ConsulDiffConfigPath, err)
		}
		// 解析配置文件到结构体
		if err := viper.Unmarshal(Config); err != nil {
			return errors.Errorf("Error unmarshaling config file: %s", err)
		}
		return nil
	}
	return nil
}

// GetInstance get consul instance
func GetInstance(opt consulUtils.InstanceOptions) *consulInst.Instance {
	// 组装实例
	client, err := consulInst.NewInstance(context.TODO(), opt)
	if err != nil {
		fmt.Printf("get consul client error, %v", err)
		os.Exit(1)
	}
	return client
}
