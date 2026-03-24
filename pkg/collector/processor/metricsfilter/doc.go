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
        - metrics: ["rpc_client_handled_total","rpc_client_dropped_total"]
          rules:
            - label: "callee_method"
              op: "in"
              values: ["hello"]
            - label: "callee_service"
              op: "in"
              values: ["example.greeter"]
            - label: "code"
              op: "range"
              values:
                - prefix: "err_"
                  min: 10
                  max: 19
                - prefix: "trpc_"
                  min: 11
                  max: 12
                - prefix: "ret_"
                  min: 100
                  max: 200
                - min: 200
                  max: 200
          target:
            action: "upsert"
            label: "code_type"
            value: "success"

      # CodeRelabel Action
      code_relabel:
        - metrics: ["rpc_client_handled_total","rpc_client_dropped_total"]
          source: "my.service.name"
          services:
          - name: "my.server;my.service;my.method"
            codes:
            - rule: "err_200~300"
              target:
                 action: "upsert"
                 label: "code_type"
                 value: "success"
            - rule: "err_400~500"
              target:
                 action: "upsert"
                 label: "code_type"
                 value: "error"
            - rule: "600"
              target:
                 action: "upsert"
                 label: "code_type"
                 value: "normal"
*/

package metricsfilter
