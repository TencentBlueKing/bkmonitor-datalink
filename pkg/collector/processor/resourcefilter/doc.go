// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
# ResourceFilter: resource 过滤器

processor:
    # Drop Action
    - name: "resource_filter/drop"
      config:
        drop:
          keys:
            - "resource.service.name"
            - "resource.service.sdk"

    # Add Action
    - name: "resource_filter/add"
      config:
        add:
          # [{label: value1, label: value2}, ...]
          - label: "fake_new_key"
            value: "fake_new_value"

    # Replace Action
    - name: "resource_filter/replace"
      config:
        replace:
          # [{source: label_src, destination: label_dst}, ...]
          - source: "telemetry.sdk.version"
            destination: "telemetry.bksdk.version"

    # Assemble Action
    - name: "resource_filter/assemble"
      config:
        assemble:
          - destination: "bk.instance.id" # 转换后名称
            separator: ":"
            keys:
              - "resource.telemetry.sdk.language"
              - "resource.service.name"
              - "resource.net.host.name"
              - "resource.net.host.ip"
              - "resource.net.host.port"

    # FromCache Action
    - name: "resource_filter/from_cache"
      config:
        from_cache:
          key: "resource.net.host.ip|resource.client.ip"
          cache_name: "k8s_cache"

    # FromRecord Action
    - name: "resource_filter/from_record"
      config:
        from_record:
          - source: "request.client.ip"
            destination: "resource.client.ip"

    # FromMetadata Action
    - name: "resource_filter/from_metadata"
      config:
        from_metadata:
          keys: ["*"]

	# FromToken Action
	- name: "resource_filter/from_token"
	  config:
		from_token:
		  keys: "app_name"

    # DefaultValue Action
    - name: "resource_filter/default_value"
      config:
        default_value:
          - type: string
            key: resource.service.name
            value: "unknown_service"

    # KeepOriginTraceId Action
    - name: "resource_filter/keep_origin_traceid"
	  config:
		keep_origin_traceid:
		  enabled: true
*/

package resourcefilter
