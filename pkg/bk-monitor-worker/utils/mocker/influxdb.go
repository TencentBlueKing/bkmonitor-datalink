// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mocker

import (
	"time"

	client "github.com/influxdata/influxdb1-client/v2"
)

type InfluxDBClientMocker struct {
	client.Client
}

func (i *InfluxDBClientMocker) Ping(timeout time.Duration) (time.Duration, string, error) {
	return 0, "", nil
}

func (i *InfluxDBClientMocker) Close() error { return nil }
