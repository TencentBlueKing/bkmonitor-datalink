// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
# DbFilter: db 数据处理器

processor:
  # 慢查询处理
  - name: "db_filter/common"
    config:
      slow_query:
        destination: "db.is_slow"
        rules:
          - match: "mysql"
            threshold: 1s
          - match: "redis"
            threshold: 2s
          - match: ""
            threshold: 3s
*/

package dbfilter
