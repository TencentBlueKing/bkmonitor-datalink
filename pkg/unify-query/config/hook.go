// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/credential"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
)

var configLock sync.Mutex
var activeSnapshot *credential.Snapshot

// InitConfig 初始化配置
func InitConfig() {
	_ = InitConfigWithWriter(os.Stdout)
}

// InitConfigWithWriter 初始化配置，并将加载提示写入指定输出，供 CLI 保持 stdout 为数据。
func InitConfigWithWriter(output io.Writer) error {
	return loadConfig(output, false)
}

// ReloadConfigWithWriter preserves the credential snapshot and rejects invalid candidates.
func ReloadConfigWithWriter(output io.Writer) error {
	return loadConfig(output, true)
}

func loadConfig(output io.Writer, reload bool) error {
	configLock.Lock()
	defer configLock.Unlock()
	// Viper 1.15 ReadInConfig/ReadConfig replace only the candidate config map.
	// Keep existing defaults, flags and env bindings; never Set on the candidate
	// before validation because those registers are shared by this value copy.
	candidate := *viper.GetViper()
	if CustomConfigFilePath != "" {
		// Use config file from the flag.
		candidate.SetConfigFile(CustomConfigFilePath)
	} else {
		// Find home directory.
		home, err := homedir.Dir()
		if err != nil {
			return fmt.Errorf("find config home directory failed")
		}

		// Search config in home directory with name ".kafka-watcher" (without extension).
		candidate.AddConfigPath(home)
		candidate.SetConfigName(fmt.Sprintf("./%s.yaml", AppName))
	}

	candidate.SetEnvPrefix("unify-query")
	candidate.AutomaticEnv() // read in environment variables that match
	candidate.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// If a config file is found, read it in.
	// 在配置文件读取前，需要先通知全世界做好准备
	if !reload {
		eventbus.EventBus.Publish(eventbus.EventSignalConfigPreParse)
	}
	if err := candidate.ReadInConfig(); err != nil {
		return fmt.Errorf("load config file failed")
	}
	// Parse the same bytes in a file-only Viper for explicit-input checks;
	// candidate Get includes env/defaults and, on reload, frozen overrides.
	data, err := os.ReadFile(candidate.ConfigFileUsed())
	if err != nil {
		return fmt.Errorf("read config file failed")
	}
	raw := viper.New()
	raw.SetConfigFile(candidate.ConfigFileUsed())
	if err := raw.ReadConfig(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("parse config file failed")
	}
	if err := candidate.ReadConfig(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("parse config file failed")
	}
	snapshot := activeSnapshot
	if reload {
		if snapshot == nil {
			return fmt.Errorf("config must initialize before reload")
		}
		snapshot, err = snapshot.Reuse(raw, &candidate)
		if err != nil {
			return err
		}
	} else {
		snapshot, err = credential.Load(raw, &candidate)
		if err != nil {
			return err
		}
	}
	// Publish only after all validation passes. Overrides are never changed on failure.
	*viper.GetViper() = candidate
	snapshot.Apply(viper.GetViper())
	activeSnapshot = snapshot
	fmt.Fprintln(output, "Using config file:", candidate.ConfigFileUsed())
	if snapshot.Enabled() {
		fmt.Fprintln(output, "KMS credentials loaded: schema_version=1")
	}
	// 配置读取后，通知全世界reload读取新的配置
	eventbus.EventBus.Publish(eventbus.EventSignalConfigPostParse)
	return err
}
