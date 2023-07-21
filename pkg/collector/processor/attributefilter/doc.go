// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
# AttributeFilter: 属性处理器 支持 as_string/from_token/assemble

processor:
   - name: "attribute_filter/common"
     config:
       # 将 attributes 部分字段转换为 string 类型
       as_string:
         keys:
           - "attributes.http.host"
       # 将 token 字段写入到 attribute 里面

       from_token:
         biz_id: "bk_biz_id"
         app_name: "bk_app_name"

       # 拼接 span 属性，插入到 attributes 中
       assemble:
         - destination: "api_name"                     # 期望插入的字段
           predicate_key: "attributes.http.scheme"     # 需要匹配到的 attributes 中的字段
             rules:
              - kind: "SPAN_KIND_CLIENT"               # 所需 Kind 的条件
                first_upper:
                  - "attributes.http.method"           # 需要大写的属性
                keys:                                  # 拼接的key
                  - "attributes.http.method"
                  - "attributes.http.host"
                  - "attributes.http.target"
                  - "const.consumer"                   # 支持常量
                separator: ":"                         # 拼接符号
              - kind: "SPAN_KIND_SERVER"
                keys:
                  - "attributes.http.method"
                  - "attributes.http.route"
                separator: ":"
*/

package attributefilter
