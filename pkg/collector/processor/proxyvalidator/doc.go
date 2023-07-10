// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
# ProxyValidator: proxy 数据校验器

processor:
    - name: "proxy_validator/time_series"
      config:
        type: time_series
        version: v2
        max_future_time_offset: 3600

    - name: "proxy_validator/event"
      config:
        type: event
        version: v2
*/

package proxyvalidator
