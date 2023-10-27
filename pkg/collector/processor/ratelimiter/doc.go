// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
# RateLimiter: 限流器

processor:
  # 令牌桶限流器
  - name: "rate_limiter/token_bucket"
    config:
      type: token_bucket
      qps: 5
      burst: 10

  # 不做限制
  - name: "rate_limiter/noop"
    config:
      type: noop
*/

package ratelimiter
