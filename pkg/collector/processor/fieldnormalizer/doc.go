// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
# 字段标准化
processor:
  - name: "field_normalizer/common"
    config:
      fields:
        - kind: "SPAN_KIND_SERVER"
          predicate_key: "attributes.http.method"
          rules:
            - key: "attributes.net.peer.name"
              op: concat
              values:
                - "attributes.client.address"
                - "attributes.client.port"

        - kind: "SPAN_KIND_CLIENT"
          predicate_key: "attributes.http.method"
          rules:
            - key: "attributes.net.peer.ip"
              op: or
              values:
                - "attributes.client.address"
                - "attributes.net.peer.address"
*/

package fieldnormalizer
