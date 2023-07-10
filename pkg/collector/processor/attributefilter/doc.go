// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
# AttributeFilter: 属性处理器

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
*/

package attributefilter
