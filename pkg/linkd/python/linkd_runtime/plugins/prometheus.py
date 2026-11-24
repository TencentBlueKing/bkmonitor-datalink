# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

import hashlib

from linkd_runtime.base import BaseClientV2


PROMETHEUS_LEVEL_DICT = {
    "1": "fatal",
    "2": "warning",
    "3": "remind",
}


class AlarmClient(BaseClientV2):
    """主动推送消息"""

    # 定义是否通过ip拿到主机在cmdb中的信息

    def __init__(self, alarm_source_obj, data):
        super().__init__(alarm_source_obj)
        self.data = data
        self._source_records = data.get("alerts", [])

    @staticmethod
    def _source_item(alarm):
        return alarm["annotations"].get("summary", "")

    @staticmethod
    def _source_alarm_time(alarm):
        return alarm["startsAt"]

    @staticmethod
    def _source_name(alarm):
        return alarm["annotations"].get("summary", "")

    @staticmethod
    def _source_event_id(alarm):
        labels = alarm.get("labels", {})
        return hashlib.md5("{}{}".format(labels.get("ip", ""), labels.get("alertname", "")).encode("utf8")).hexdigest()

    @staticmethod
    def _source_content(alarm):
        return alarm["annotations"].get("description", "")

    @staticmethod
    def _source_level(alarm):
        return PROMETHEUS_LEVEL_DICT.get(alarm.get("labels", {}).get("severity", "3"))

    @staticmethod
    def _source_action(alarm):
        return alarm.get("status", "firing")

    @staticmethod
    def _source_object(alarm):
        return alarm.get("labels", {}).get("ip", "")

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
        event["fingerprint"] = alarm.get("fingerprint") or event["fingerprint"]
        event["dimensions"] = dict(alarm.get("labels") or {})
        if action == "resolved":
            event["occurred_at"] = self.source_time(alarm["endsAt"])
        if extra.get("object"):
            event["subject"] = {"name": str(extra["object"])}
        return event
