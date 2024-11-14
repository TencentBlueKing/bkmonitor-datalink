// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
# TokenChecker: Token 校验器

processor:
  # 固定 token
  - name: "token_checker/fixed"
    config:
      type: "fixed"
      fixed_token: "token1"
      resource_key: "bk.data.token"
      traces_dataid: 1000
      metrics_dataid: 1001
      logs_dataid: 1002

  # proxy token 校验规则
  - name: "token_checker/proxy"
    config:
      token: "xxxxxxx"
      dataid: 1001

  # ase256 校验规则
  - name: "token_checker/aes256"
    config:
      type: "aes256"
      resource_key: "bk.data.token"
      salt: "bk" # 加盐解密标识
      decoded_iv: "bkbkbkbkbkbkbkbk"
      decoded_key: "81be7fc6-5476-4934-9417-6d4d593728db"

  # aes256+子配置 dataid 校验规则
  - name: "token_checker/aes256WithMeta"
    config:
      type: "aes256WithMeta"
      resource_key: "bk.data.token"
      salt: "bk" # 加盐解密标识
      decoded_iv: "bkbkbkbkbkbkbkbk"
      decoded_key: "81be7fc6-5476-4934-9417-6d4d593728db"

  # aes256+子配置 dataid 校验规则
  - name: "token_checker/combine"
    config:
      type: "aes256WithMeta|fixed"
      # aes256 配置
      resource_key: "bk.data.token"
      salt: "bk" # 加盐解密标识
      decoded_iv: "bkbkbkbkbkbkbkbk"
      decoded_key: "81be7fc6-5476-4934-9417-6d4d593728db"
      # fixed 配置
      fixed_token: foobar
      traces_dataid: 1000
      metrics_dataid: 1001
      logs_dataid: 1002
      profiles_dataid: 1002
*/

package tokenchecker
