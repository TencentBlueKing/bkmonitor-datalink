// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
# MetricsFilter: 指标过滤器

processor:
  - name: "metrics_filter/drop"
    config:
      # Drop Action
      drop:
        metrics:
        - "runtime.go.mem.live_objects"
        - "none.exist.metric"

      # Replace Action
      replace:
        - source: "previous_metric"       # 原字段
          destination: "current_metric"   # 新字段

      # Relabel Action
      relabel:
        - metrics:
          - "test_metric1"
          - "test_metric2"
          rules:						# 规则之间为 && 关系
            - label: "label1"			# 字段名
              op: "in"					# 操作符，支持 in, notin, range
              values: ["val1", "val2"]	# in, notin 操作时，values 为字符串列表，range 操作时，values 为范围列表
            - label: "code"
              op: "range"
              values:
                - prefix: "err_"			# 前缀可为空
                  min: 10
                  max: 19
          destinations:
            - action: "upsert"				# 操作，目前支持覆写
              label: "code_type"			# 要插入/覆盖的字段
              value: "success"
*/

package metricsfilter
