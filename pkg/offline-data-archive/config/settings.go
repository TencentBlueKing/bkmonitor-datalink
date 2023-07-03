// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

const (
	AppName = "offline-data-archive"

	MoveInstanceNameConfigPath         = "move.instance_name"
	MoveClusterNameConfigPath          = "move.cluster_name"
	MoveTagNameConfigPath              = "move.tag_name"
	MoveTagValueConfigPath             = "move.tag_value"
	MoveSourceDirConfigPath            = "move.source_dir"
	MoveTargetNameConfigPath           = "move.target_name"
	MoveTargetDirConfigPath            = "move.target_dir"
	MoveMaxPoolConfigPath              = "move.max_pool"
	MoveIntervalConfigPath             = "move.interval"
	MoveDistributedLockExpiration      = "move.distribute_lock.expiration"
	MoveDistributedLockRenewalDuration = "move.distribute_lock.renewal_duration"
	MoveInfluxDBAddressConfigPath      = "move.influxdb.address"
	MoveInfluxDBUserNameConfigPath     = "move.influxdb.username"
	MoveInfluxDBPasswordConfigPath     = "move.influxdb.password"

	RebuildFinalNameConfigPath            = "rebuild.final_name"
	RebuildFinalDirConfigPath             = "rebuild.final_dir"
	RebuildMaxPoolConfigPath              = "rebuild.max_pool"
	RebuildIntervalConfigPath             = "rebuild.interval"
	RebuildDistributedLockExpiration      = "rebuild.distribute_lock.expiration"
	RebuildDistributedLockRenewalDuration = "rebuild.distribute_lock.renewal_duration"

	QueryHttpHostConfigPath        = "query.http.host"
	QueryHttpPortConfigPath        = "query.http.port"
	QueryHttpReadTimeoutConfigPath = "query.http.read_timeout"
	QueryHttpMetricConfigPath      = "query.http.metric"
	QueryHttpDIrConfigPath         = "query.http.dir"
)

var (
	Version    = "0.1.0"
	CommitHash = "unknown"

	// 自定义配置路径
	CustomConfigFilePath string
)
