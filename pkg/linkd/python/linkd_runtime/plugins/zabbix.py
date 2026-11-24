# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""Zabbix 推送转换与注入的来源类型映射。"""
import base64
import json
import re
from fnmatch import fnmatch

from linkd_runtime.base import BaseClientV2


class AlarmClient(BaseClientV2):
    """zabbix主动推送消息"""

    # 定义是否通过ip拿到主机在cmdb中的信息

    def __init__(self, alarm_source_obj, data):
        super().__init__(alarm_source_obj)
        # 原 schema 只有 object 和六个 required 字段，使用等价标准库检查即可。
        required = ("ip", "source_id", "source_time", "alarm_type", "alarm_content", "alarm_raw")
        if not isinstance(data, dict) or any(key not in data for key in required):
            raise ValueError("Zabbix payload requires ip/source_id/source_time/alarm_type/alarm_content/alarm_raw")
        data = dict(data)
        self.data = data

        # 数据格式化
        data = self._data_format(data, ["alarm_content", "alarm_raw"], format_patten=data.get("format"), replace=True)
        data = self._data_format(data, ["alarm_raw"], format_patten="json", replace=True)

        # 增加event_id
        data["event_id"] = data["source_id"].split("-")[1] + "-" + data["ip"]
        self._source_records = [data]

    @staticmethod
    def _data_format(data, fields, format_patten="", replace=False):
        """数据格式化"""
        for field in fields:
            value = data[field]
            try:
                if format_patten == "json":
                    _source_value = json.loads(value)
                elif format_patten == "base64":
                    _source_value = base64.b64decode(value).decode("utf8")
                else:
                    _source_value = value
            except Exception:
                raise ValueError("The field [{}] is in invalid {} format".format(field, format_patten))
            else:
                if replace:
                    data[field] = _source_value
        return data

    def _source_item(self, alarm):
        value = alarm["alarm_type"]
        names = []
        for rule in self.alarm_source_config_dict.get("alarm_types", []):
            mode, pattern = rule["match_mode"], rule["match_patten"]
            matched = (mode == "string" and value == pattern) or (mode == "regular" and re.match(pattern, value)) or (mode == "wildcard" and fnmatch(value, pattern))
            if matched:
                names.append(rule["name"])
        return ",".join(names) if names else value

    @staticmethod
    def _source_alarm_time(alarm):
        return alarm["source_time"]

    @staticmethod
    def _source_name(alarm):
        return alarm["alarm_content"]

    @staticmethod
    def _source_event_id(alarm):
        return alarm["event_id"]

    @staticmethod
    def _source_content(alarm):
        alarm_raw = alarm["alarm_raw"]
        return "{}, the value is {}".format(alarm_raw.get("ITEM.KEY"), alarm_raw.get("ITEM.VALUE"))

    @staticmethod
    def _source_level(alarm):
        level = alarm["alarm_raw"].get("TRIGGER.SEVERITY", "Not classified")
        if level in ["Not classified", "Information"]:
            level = 'remind'
        elif level in ["Warning", "Average"]:
            level = 'warning'
        else:  # ["High", "Disaster"]
            level = 'fatal'
        return level

    @staticmethod
    def _source_action(alarm):
        event_status = alarm["event_status"]
        action = 'resolved' if event_status == "RESOLVED" else 'firing'
        return action

    @staticmethod
    def _source_object(alarm):
        return alarm.get("ip", "")

    def pull_raw_events(self):
        return [self._standard_event(alarm) for alarm in self._source_records]

    def _standard_event(self, alarm):
        # 1. 第三方来源身份直接用于关联，发生时间和事件编号不替代 fingerprint。
        fingerprint = self._source_event_id(alarm)
        if fingerprint is None or fingerprint == "":
            raise ValueError("来源缺少稳定告警身份")
        action = self._source_action(alarm)
        action = {"firing": "triggered", "close": "closed"}.get(action, action)
        if action == "heartbeat":
            return {"action": "heartbeat"}
        extra = dict(alarm)
        extra["item"] = self._source_item(alarm)
        extra["object"] = self._source_object(alarm)
        # 2. 扩展事实保留给 Linkd 清洗和丰富，标准字段不再经旧告警结构转接。
        event = {
            "fingerprint": str(fingerprint),
            "title": str(self._source_name(alarm) or self.alarm_source_obj.name),
            "content": self._source_content(alarm) or "",
            "evaluations": [{"action": action, "severity": str(self._source_level(alarm) or "warning")}],
            "occurred_at": self.source_time(self._source_alarm_time(alarm)),
            "dimensions": dict(alarm.get("dimensions") or {}),
            "labels": dict(alarm.get("labels") or {}),
            "extra_data": extra,
        }
        if extra.get("object"):
            event["subject"] = {"name": str(extra["object"])}
        return event
