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
	"fmt"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	ClusterMetricStorageKeyPrefix string
	ClusterMetricStorageTTL       int
	ClusterMetricKey              string
	ClusterMetricMetaKey          string
	ClusterMetricSubKeyPattern    string
	ClusterMetricClusterFieldName string
	ClusterMetricFieldName        string
	ClusterMetricHostFieldName    string
	ESClusterMetricTarget         string
	ESClusterMetricQueueName      string

	RabbitMQClusterMetricEnabled           bool
	RabbitMQClusterMetricTarget            string
	RabbitMQClusterMetricQueueName         string
	RabbitMQClusterMetricReportUrl         string
	RabbitMQClusterMetricReportDataId      int
	RabbitMQClusterMetricReportAccessToken string
	RabbitMQClusterMetricInstances         []RabbitMQClusterMetricInstance
)

type RabbitMQClusterMetricInstance struct {
	Name                  string   `mapstructure:"name"`
	Schema                string   `mapstructure:"schema"`
	DomainName            string   `mapstructure:"domainName"`
	HTTPPort              int      `mapstructure:"httpPort"`
	AMQPPort              int      `mapstructure:"amqpPort"`
	Username              string   `mapstructure:"username"`
	Password              string   `mapstructure:"password"`
	Vhosts                []string `mapstructure:"vhosts"`
	QueueIncludes         []string `mapstructure:"queueIncludes"`
	QueueExcludes         []string `mapstructure:"queueExcludes"`
	QueueIncludeRegexes   []string `mapstructure:"queueIncludeRegexes"`
	QueueExcludeRegexes   []string `mapstructure:"queueExcludeRegexes"`
	BkBizID               int      `mapstructure:"bkBizId"`
	BkTenantID            string   `mapstructure:"bkTenantId"`
	TimeoutSeconds        int      `mapstructure:"timeoutSeconds"`
	TLSInsecureSkipVerify bool     `mapstructure:"tlsInsecureSkipVerify"`
}

func initClusterMetricVariables() {
	ClusterMetricStorageKeyPrefix = GetValue("taskConfig.cluster_metrics.storage_key_prefix", "bkmonitor")
	ClusterMetricStorageTTL = GetValue("taskConfig.cluster_metrics.storage_ttl", 300)
	ClusterMetricKey = fmt.Sprintf("%s:cluster_metrics", ClusterMetricStorageKeyPrefix)
	ClusterMetricMetaKey = fmt.Sprintf("%s:cluster_metrics_meta", ClusterMetricStorageKeyPrefix)

	ClusterMetricSubKeyPattern = "{bkm_metric_name}|bkm_cluster={bkm_cluster}"
	ClusterMetricClusterFieldName = "bkm_cluster"
	ClusterMetricFieldName = "bkm_metric_name"
	ClusterMetricHostFieldName = "bkm_hostname"

	ESClusterMetricTarget = "bk_log_search"
	ESClusterMetricQueueName = GetValue("taskConfig.logSearch.queueName", "log-search")

	RabbitMQClusterMetricEnabled = getRabbitMQBool("taskConfig.rabbitmqMetric.enabled", true)
	RabbitMQClusterMetricTarget = getRabbitMQString("taskConfig.rabbitmqMetric.target", "bk_rabbitmq")
	RabbitMQClusterMetricQueueName = getRabbitMQString("taskConfig.rabbitmqMetric.queueName", "default")
	RabbitMQClusterMetricReportUrl = getRabbitMQString("taskConfig.rabbitmqMetric.reportUrl", "")
	RabbitMQClusterMetricReportDataId = getRabbitMQInt("taskConfig.rabbitmqMetric.reportDataId", 0)
	RabbitMQClusterMetricReportAccessToken = getRabbitMQString("taskConfig.rabbitmqMetric.reportAccessToken", "")
	RabbitMQClusterMetricInstances = getRabbitMQInstances("taskConfig.rabbitmqMetric.instances")
}

func getRabbitMQBool(key string, def bool) bool {
	if !viper.IsSet(key) {
		return def
	}
	return viper.GetBool(key)
}

func getRabbitMQString(key string, def string) string {
	if !viper.IsSet(key) {
		return def
	}
	return viper.GetString(key)
}

func getRabbitMQInt(key string, def int) int {
	if !viper.IsSet(key) {
		return def
	}
	return viper.GetInt(key)
}

func getRabbitMQInstances(key string) []RabbitMQClusterMetricInstance {
	var instances []RabbitMQClusterMetricInstance
	if !viper.IsSet(key) {
		return instances
	}
	if err := viper.UnmarshalKey(key, &instances); err != nil {
		logger.Errorf("failed to unmarshal rabbitmq cluster metric instances: %v", err)
		return []RabbitMQClusterMetricInstance{}
	}
	return instances
}
