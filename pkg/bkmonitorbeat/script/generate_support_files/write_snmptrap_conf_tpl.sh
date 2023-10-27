#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.


write_snmptrap_conf_tpl() {
  path="$1"
  system="$2"
  arch="$3"
  cat <<EOF > "$path"
EOF
  cat <<EOF >> "$path"
# SNMP Trap采集配置模板
type: snmptrap
name: {{ config_name | default("snmptrap_task", true) }}
version: {{ config_version| default("1.1.1", true) }}

# 配置框架需要，这里补充0，实际dataid在tasks下
dataid: 0

tasks: {% for task in tasks %}
   - task_id: {{ task.task_id }}
     bk_biz_id: {{ task.bk_biz_id }}
     dataid: {{ task.dataid | int }}
     target: {{ task.target }}
     # 团体名
     community: {{ task.community }}
     # 监听ip
     listen_ip: {{ task.listen_ip }}
     # 监听端口
     listen_port: {{ task.listen_port }}
     # trap版本
     snmp_version: {{ task.snmp_version }}
     # 是否聚合
     aggregate: {{ task.aggregate }}
     # 聚合周期，保留参数，实际使用采集周期作为聚合周期
     period: {{ task.period | default('1m', true) }}
     # oid事件指标map
     oids: {% for key, value in task.oids.items() %}
        "{{ key }}": "{{ value }}"{% endfor %}
     # ==============  下面字段为 v3 专享  ==========
     # 多用户配置
     usm_info: {% for usm in task.usm_info %}
        # 上下文信息
        - context_name: {{ usm.context_name }}
        # 消息标识位，authpriv authnopriv noauthnopriv三种
          msg_flags: {{ usm.msg_flags }}
        # USM配置信息
          usm_config:
             username: {{ usm.usm_config.username }}
             # noauth, md5, sha, sha224, sha256, sha384, sha512  可选
             authentication_protocol: {{ usm.usm_config.authentication_protocol }}
             authentication_passphrase: {{ usm.usm_config.authentication_passphrase }}
             # nopriv, des, aes, aes192, aes256, aes192c, aes256c 可选
             privacy_protocol: {{ usm.usm_config.privacy_protocol }}
             privacy_passphrase: {{ usm.usm_config.privacy_passphrase }}
             authoritative_engineID: {{ usm.usm_config.authoritative_engineID }}
             # engineboots和enginetime为保留参数，默认为1
             authoritative_engineboots: {{ usm.usm_config.authoritative_engineboots }}
             authoritative_enginetime: {{ usm.usm_config.authoritative_enginetime }}{% endfor %}
     # 注入的labels
     labels: {% for label in task.labels %}
        {% for key, value in label.items() %}{{ "-" if loop.first else " "  }} {{ key }}: "{{ value }}"
        {% endfor %}{% endfor %}
{% endfor %}
EOF
}
