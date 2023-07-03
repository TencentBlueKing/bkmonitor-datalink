// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/conv"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/elasticsearch"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/esb"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/filesystem/processor"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/influxdb"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/kafka"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/redis"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/scheduler"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/shipper"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/shipper/echo"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/shipper/noop"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/storage"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/auto"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/basereport"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/exporter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/flat"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/formatter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/fta"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/log"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/procperf"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/procport"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/standard"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/uptimecheck"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/pipeline"
)
