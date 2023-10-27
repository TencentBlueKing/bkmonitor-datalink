// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
# ProbeFilter: 探针根据配置上报数据处理器

processor:
  - name: "probe_filter/common"
    config:
      add_attributes:
        - rules:
          - type: "Http"                         # span 的类型
            enabled: true                        # 是否启用
            target: "cookie"                     # 采集数据的目标
            field: "language"                    # 需要采集的字段的 key
            prefix: "custom_tag"                 # 需要插入字段的前缀
            filters:
              - field: "resource.service.name"
                value: "account"
                type: "service"
              - field: "attributes.api_name"
                value: "POST:/account/pay"
                type: "interface"
*/

package probefilter
