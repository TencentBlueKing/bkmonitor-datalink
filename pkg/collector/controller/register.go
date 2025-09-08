// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controller

import (
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/outputdropper"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/apdexcalculator"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/attributefilter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/dbfilter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/licensechecker"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/metricsfilter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/pproftranslator"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/probefilter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/proxyvalidator"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/ratelimiter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/resourcefilter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/sampler"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/servicediscover"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/textspliter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/tokenchecker"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/tracesderiver"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/beat"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/fta"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/jaeger"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/logpush"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/otlp"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/pushgateway"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/pyroscope"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/remotewrite"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/skywalking"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/tars"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/zipkin"
)
