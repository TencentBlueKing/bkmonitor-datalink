// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trace

import (
	"context"
	"fmt"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// setDefaultConfig
func setDefaultConfig() {
	viper.SetDefault(KeysConfigPath, nil)
	viper.SetDefault(OtlpHostConfigPath, "127.0.0.1")
	viper.SetDefault(OtlpPortConfigPath, "4317")
	viper.SetDefault(OtlpTokenConfigPath, "")
	viper.SetDefault(OtlpTypeConfigPath, "grpc")

	viper.SetDefault(ServiceNameConfigPath, "unify-query")
	viper.SetDefault(EnableConfigPath, true)
}

// InitConfig
func InitConfig() {

	Enable = viper.GetBool(EnableConfigPath)

	for key, value := range configLabels {
		log.Debugf(context.TODO(), "key->[%s] value->[%s] now is added to labels", key, value)
		labels[key] = value
	}

	otlpHost = viper.GetString(OtlpHostConfigPath)
	otlpPort = viper.GetString(OtlpPortConfigPath)
	otlpToken = viper.GetString(OtlpTokenConfigPath)
	log.Infof(context.TODO(), "trace will Otlp to host->[%s] port->[%s] token->[%s]", otlpHost, otlpPort, otlpToken)

	OtlpType = viper.GetString(OtlpTypeConfigPath)
	log.Infof(context.TODO(), "trace will Otlp as %s type", OtlpType)

	ServiceName = viper.GetString(ServiceNameConfigPath)
	log.Infof(context.TODO(), "trace will Otlp service name:%s", ServiceName)
}

// init
func init() {
	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPreParse, setDefaultConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for trace module for default config, maybe http module won't working.",
			eventbus.EventSignalConfigPreParse,
		)
	}

	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPostParse, InitConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for trace module for new config, maybe http module won't working.",
			eventbus.EventSignalConfigPostParse,
		)
	}
}
