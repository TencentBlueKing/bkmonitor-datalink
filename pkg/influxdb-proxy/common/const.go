// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package common

// kafka 配置项
const (
	ConfigKeyKafkaAddress     = "kafka.address"
	ConfigKeyKafkaPort        = "kafka.port"
	ConfigKeyKafkaTopicPrefix = "kafka.topic_prefix"
	ConfigKeyKafkaVersion     = "kafka.version"
	ConfigKeyKafkaUsername    = "kafka.username"
	ConfigKeyKafkaPassword    = "kafka.password"
	ConfigKeyKafkaIsAuth      = "kafka.is_auth_enable"
	ConfigKeyKafkaMechanism   = "kafka.mechanism"
	ConfigKeyKafkaRetention   = "kafka.offset_retention"
)

// ConfigKeyBackendForceBackup :
const (
	ConfigKeyBackendForceBackup  = "backend.force_backup"
	ConfigKeyBackendTimeout      = "backend.timeout"
	ConfigKeyBackendIgnoreKafka  = "backend.ignore_kafka"
	ConfigKeyBatchSize           = "backend.batch_size"
	ConfigKeyFlushTime           = "backend.flush_time"
	ConfigKeyMaxFlushConcurrency = "backend.max_flush_concurrency"
)

const (
	ConfigHTTPPort      = "http.port"
	ConfigHTTPAddress   = "http.listen"
	ConfigHTTPBatchsize = "batch_size"

	ConfigKeyConsulHealthPeriod      = "consul.health.period"
	ConfigKeyConsulHealthServiceName = "consul.health.service_name"
	ConfigKeyConsulAddress           = "consul.address"
	ConfigKeyConsulPrefix            = "consul.prefix"
	ConfigKeyConsulCACertFile        = "consul.ca_file_path"
	ConfigKeyConsulCertFile          = "consul.cert_file_path"
	ConfigKeyConsulKeyFile           = "consul.key_file_path"
	ConfigKeyConsulSkipVerify        = "consul.skip_verify"
)
