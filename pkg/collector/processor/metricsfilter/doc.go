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
      # metrics: metric name
      metrics:
        - "runtime.go.mem.live_objects"
        - "none.exist.metric"
        # Replace Action
      replace:
        - source: "previous_metric"       # 原字段
          destination: "current_metric"   # 新字段
      relabel:
        - metric: "test_metric"
          rules:
            - label: "label1"
              op: "in"
              values: ["value1", "value2"]
            - label: "code"
              op: "range"
              values:
                - prefix: "err_"
                  min: 10
                  max: 19
          destinations:
            - action: "upsert"
              label: "code_type"
              value: "success"
*/

package metricsfilter
