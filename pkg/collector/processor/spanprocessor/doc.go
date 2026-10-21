// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
# Span Processor: span 数据处理器

processor:
  - name: "span_processor/common"
    config:
      drop:
        - match_rules:
            - key: "span_name"
			  op: "eq"
              value:
                - "xxx"
              link: "and"
      replace_value:
        - predicate_key: "span_name"
          rules:
            - filters:
                - key: "span_name"
                  op: "eq"
                  value:
                    - "xxx"
                  link: "and"
              replace_from:
                source:
                  - "span_name"
                separator: ":"
                const_val: "unknown"
*/

package spanprocessor
