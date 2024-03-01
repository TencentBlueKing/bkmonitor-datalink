// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
# Sampler: 采样器

processor:
  # 概率采样
  - name: "sampler/random"
    config:
      type: "random"
      sampling_percentage: 100 # 采样率 [0, 100]

  # 永远采样
  - name: "sampler/always"
    config:
      type: "always"

  # 拒绝采样
  - name: "sampler/drop_xxx"
    config:
      type: "drop"
      enabled: false

  - name: "sampler/status_code"
    config:
      type: "status_code"
      # traces 存储策略
      # full: 表示存储所有数据
      # post: 只存储 traceID/spanID
      storage_policy: "full"
      max_spans: 100 # 每个 traces 最多允许的 spans 数量
      status_code: # ERROR|OK|UNSET
      - "ERROR"
*/

package sampler
