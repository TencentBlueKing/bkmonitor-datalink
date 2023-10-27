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
	"fmt"
	"math"
	"os"
	"runtime"

	"github.com/cstockton/go-conv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

func getLimitsValue(limits float64, max int) int {
	if math.IsInf(limits, 1) || math.IsNaN(limits) {
		return 0
	}

	value := int(limits)
	// relative
	if value < 1 {
		value = int(float64(max) * limits)
	}
	return value
}

func init() {
	utils.CheckError(eventbus.SubscribeAsync(eventbus.EvSysLimitCPU, func(limits float64) {
		max := getLimitsValue(limits, runtime.NumCPU())

		if max > 0 {
			_, err := fmt.Fprintf(os.Stderr, "cpus limited to %d\n", max)
			utils.CheckError(err)
			runtime.GOMAXPROCS(max)
		}
	}, false))

	utils.CheckError(eventbus.SubscribeAsync(eventbus.EvSigLimitResource, func(params map[string]string) {
		logging.Warnf("resource limits activated by signal")

		for key, value := range params {
			limits, err := conv.DefaultConv.Float64(value)
			if err != nil {
				logging.Errorf("resource %s value expect float but got %v, %v", key, value, err)
				continue
			}

			switch key {
			case "cpu":
				eventbus.Publish(eventbus.EvSysLimitCPU, limits)
			case "files":
				eventbus.Publish(eventbus.EvSysLimitFile, limits)
			case "memory":
				eventbus.Publish(eventbus.EvSysLimitMemory, limits)
			default:
				logging.Warnf("unknown resource %s", key)
			}
		}
	}, false))
}
