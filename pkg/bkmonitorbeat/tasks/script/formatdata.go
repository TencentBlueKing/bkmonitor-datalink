// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package script

import (
	"bufio"
	"bytes"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// FormatOutput : 解析 Prom 格式数据，输出结构化数据，同时输出失败记录
func FormatOutput(out []byte, ts int64, offsetTime time.Duration, handler tasks.TimestampHandler) (map[int64]map[string]tasks.PromEvent, error) {
	// map[timestamp]map[dimension_hash]PromEvent
	aggreRst := make(map[int64]map[string]tasks.PromEvent)
	scanner := bufio.NewScanner(bytes.NewBuffer(out))
	var outputErr error

	for scanner.Scan() {
		line := scanner.Text()
		if len(line) == 0 || line[0] == '#' {
			continue
		}

		promEvent, promErr := tasks.NewPromEvent(line, ts, offsetTime, handler)
		if promErr != nil {
			logger.Warnf("parse line=>(%s) failed,error:%s", line, promErr)
			outputErr = promErr
			continue
		}

		promEvent.AggreValue[promEvent.Key] = promEvent.Value
		subRst, tsExist := aggreRst[promEvent.TS]
		if tsExist {
			p, dmExist := subRst[promEvent.HashKey]
			if dmExist {
				p.AggreValue[promEvent.Key] = promEvent.Value
				subRst[promEvent.HashKey] = p
			} else {
				subRst[promEvent.HashKey] = promEvent
			}
			aggreRst[promEvent.TS] = subRst
		} else {
			subRst = make(map[string]tasks.PromEvent, 0)
			subRst[promEvent.HashKey] = promEvent
			aggreRst[promEvent.TS] = subRst
		}
	}
	return aggreRst, outputErr
}
