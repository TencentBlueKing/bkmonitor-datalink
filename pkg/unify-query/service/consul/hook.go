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

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// setDefaultConfig
func setDefaultConfig() {
	viper.SetDefault(ServiceNameConfigPath, "")
	viper.SetDefault(KVBasePathConfigPath, "bkmonitorv3/unify-query")
	viper.SetDefault(AddressConfigPath, "http://127.0.0.1:8500")
	viper.SetDefault(TTLConfigPath, "30s")
	viper.SetDefault(TLSCertFileConfigPath, "")
	viper.SetDefault(TLSSkipVerify, false)
	viper.SetDefault(TLSCaFileConfigPath, "")
	viper.SetDefault(TLSKeyFileConfigPath, "")
	viper.SetDefault(ACLTokenConfigPath, "")
}

// LoadConfig
func LoadConfig() {

	ServiceName = viper.GetString(ServiceNameConfigPath)
	KVBasePath = viper.GetString(KVBasePathConfigPath)

	HTTPAddress = viper.GetString(HTTPAddressConfigPath)
	Port = viper.GetInt(PortConfigPath)
	TTL = viper.GetString(TTLConfigPath)

	Address = viper.GetString(AddressConfigPath)
	CaFilePath = viper.GetString(TLSCaFileConfigPath)
	KeyFilePath = viper.GetString(TLSKeyFileConfigPath)
	CertFilePath = viper.GetString(TLSCertFileConfigPath)
	SkipTLSVerify = viper.GetBool(TLSSkipVerify)
	ACLToken = viper.GetString(ACLTokenConfigPath)

	log.Debugf(context.TODO(),
		"reload success new config target service name:%s,consul address:%s,address:%s,port:%d,ttl:%s",
		ServiceName, Address, HTTPAddress, Port, TTL,
	)
}

// init
func init() {
	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPreParse, setDefaultConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for http module for default config, maybe http module won't working.",
			eventbus.EventSignalConfigPreParse,
		)
	}

	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPostParse, LoadConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for http module for new config, maybe http module won't working.",
			eventbus.EventSignalConfigPostParse,
		)
	}
}
