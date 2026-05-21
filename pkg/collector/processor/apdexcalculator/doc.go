// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
# ApdexCalculator: apdex 状态计算器

用于根据指标值或 span 耗时计算 apdex 状态，并将结果写入目标字段。

- metrics 场景下，`predicate_key` 当前支持 `attributes.*`。
- traces/rum 场景下，`predicate_key` 当前支持 `span_name`、`attributes.*`、`resource.*`。

processor:
  # 标准 apdex 计算方式
  - name: "apdex_calculator/standard"
    config:
      calculator:
        type: "standard"
      rules:
        # 1. metrics 默认规则示例：直接按指标名计算。
        - kind: ""
          metric_name: "bk_apm_duration"
          destination: "apdex_type"
          apdex_t: 20 # ms

        # 2. traces/rum 示例：当 span_name 存在且 kind 为 INTERNAL 时计算。
        - kind: "SPAN_KIND_INTERNAL"
          predicate_key: "span_name"
          destination: "rum_apdex_type"
          apdex_t: 500 # ms
          duration:
            start_event: "fetchStart"
            end_event: "loadEventEnd"

        # 3. traces/rum 示例：按 attributes 字段判断。
        - kind: "SPAN_KIND_SERVER"
          predicate_key: "attributes.http.method"
          destination: "apdex_type"
          apdex_t: 20 # ms

        # 4. traces/rum 示例：按 resource 字段判断。
        - kind: "SPAN_KIND_SERVER"
          predicate_key: "resource.service.name"
          destination: "apdex_type"
          apdex_t: 20 # ms

  # 固定 apdex 状态（测试用途）
  - name: "apdex_calculator/fixed"
    config:
      calculator:
        type: "fixed"
        apdex_status: "satisfied"
*/

package apdexcalculator
