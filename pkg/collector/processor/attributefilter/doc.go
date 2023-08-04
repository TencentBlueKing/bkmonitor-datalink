// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
# AttributeFilter: 属性处理器 支持 as_string/as_int/from_token/assemble/cut/drop

processor:
   - name: "attribute_filter/common"
     config:
       # 将 attributes 部分字段转换为 string 类型
       as_string:
         keys:
           - "attributes.http.host"

      # 将 attributes 部分字段转换为 int 类型
       as_int:
         keys:
           - "attributes.http.status_code"

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

        # 根据最大允许长度 裁剪字段
        cut:
          - predicate_key: "attributes.db.system"   # 需要预先匹配的 Key
            match:                                  # 预先匹配的 Key 的值限制的条件，属于 match 中的那种元素 无 match 条件也可
              - "mysql"
              - "postgresql"
            max_length: 512                         # 需要截断的 key 的值的长度
            keys:                                   # 所需要截断的 key
              - "attributes.db.statement"

        # 丢弃符合规则的 attributes
        drop:
          - predicate_key: "attributes.db.system"   # 需要预先匹配的 Key
            match:                                  # 限制预先匹配的 key 的值 不符合条件则不进行后续操作 无 match 条件也可
              - "mysql"
              - "postgresql"
            keys:                                   # 需要移除的key
              - "attributes.db.parameters"
*/

package attributefilter
