// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package broker

import (
	"fmt"

	"github.com/gocelery/gocelery"
	"github.com/spf13/viper"
)

const (
	rabbitmqSchemePath    = "broker.rabbitmq.scheme"
	rabbitmqHostPath      = "broker.rabbitmq.host"
	rabbitmqPortPath      = "broker.rabbitmq.port"
	rabbitmqUsernamePath  = "broker.rabbitmq.username"
	rabbitmqPasswordPath  = "broker.rabbitmq.password"
	rabbitmqVhostPath     = "broker.rabbitmq.vhost"
	rabbitmqExchangePath  = "broker.rabbitmq.exchange"
	rabbitmqQueueNamePath = "broker.rabbitmq.queue_name"
)

func setRabbitmqDefault() {
	viper.SetDefault(rabbitmqSchemePath, "amqp")
	viper.SetDefault(rabbitmqHostPath, "127.0.0.1")
	viper.SetDefault(rabbitmqPortPath, 5672)
	viper.SetDefault(rabbitmqUsernamePath, "guest")
	viper.SetDefault(rabbitmqPasswordPath, "guest")
	viper.SetDefault(rabbitmqVhostPath, "/")
	viper.SetDefault(rabbitmqExchangePath, "default")
	viper.SetDefault(rabbitmqQueueNamePath, "celery")
}

var rabbitmqUri string

func init() {
	setRabbitmqDefault()
}

func getRabbitmqUri() string {
	// 组装 rabbitmq uri
	rabbitmqUri := fmt.Sprintf(
		"%s://%s:%s@%s:%d/%s",
		viper.GetString(rabbitmqSchemePath),
		viper.GetString(rabbitmqUsernamePath),
		viper.GetString(rabbitmqPasswordPath),
		viper.GetString(rabbitmqHostPath),
		viper.GetInt(rabbitmqPortPath),
		viper.GetString(rabbitmqVhostPath),
	)
	return rabbitmqUri
}

func newRabbitmqBroker() *gocelery.AMQPCeleryBroker {
	rabbitmqUri := getRabbitmqUri()
	return gocelery.NewAMQPCeleryBroker(
		rabbitmqUri,
		viper.GetString(rabbitmqExchangePath),
		viper.GetString(rabbitmqQueueNamePath),
	)
}

func newRabbitmqBackend() *gocelery.AMQPCeleryBackend {
	rabbitmqUri := getRabbitmqUri()
	return gocelery.NewAMQPCeleryBackend(rabbitmqUri)
}
