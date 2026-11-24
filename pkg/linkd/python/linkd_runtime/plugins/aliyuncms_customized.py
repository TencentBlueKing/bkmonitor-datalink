# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

# -*- coding: utf-8 -*-
import json

from linkd_runtime.base import BaseClientV2


ALARM_LEVEL_MAPPING = {
    "INFO": "remind",
    "WARNING": "warning",
    "CRITICAL": "fatal",
}

ALARM_ACTION_MAPPING = {
    "TRIGGERED": "firing",
    "RESOLVED": "resolved",
    "OK": "resolved",
}


class AlarmClient(BaseClientV2):
    def __init__(self, alarm_source_obj, data):
        super(AlarmClient, self).__init__(alarm_source_obj)
        self.data = data
        if isinstance(data, str):
            data = json.loads(data)
        self._source_records = data if isinstance(data, list) else [data]

    @staticmethod
    def _source_item(alarm):
        alert = alarm.get("alert", {})
        return alert.get("eventType", "")

    @staticmethod
    def _source_name(alarm):
        alert = alarm.get("alert", {})
        meta = alert.get("meta", {})
        sys_event_meta = meta.get("sysEventMeta", {})
        return sys_event_meta.get("eventNameZh", "")

    @staticmethod
    def _source_event_id(alarm):
        alert = alarm.get("alert", {})
        meta = alert.get("meta", {})
        sys_event_meta = meta.get("sysEventMeta", {})
        return sys_event_meta.get("id", "")

    @staticmethod
    def _source_alarm_time(alarm):
        alert = alarm.get("alert", {})
        return alert.get("eventContentMap", {}).get("publishTime") or alert.get("timestamp")

    @staticmethod
    def _source_content(alarm):
        alert = alarm.get("alert", {})
        meta = alert.get("meta", {})
        sys_event_meta = meta.get("sysEventMeta", {})
        return sys_event_meta.get("eventNameZh", "")

    @staticmethod
    def _source_level(alarm):
        severity = alarm.get("severity", "WARNING")
        return ALARM_LEVEL_MAPPING.get(severity, "warning")

    @staticmethod
    def _source_action(alarm):
        alert = alarm.get("alert", {})
        alert_status = alert.get("alertStatus", "TRIGGERED")
        return ALARM_ACTION_MAPPING.get(alert_status, "firing")

    @staticmethod
    def _source_object(alarm):
        alert = alarm.get("alert", {})
        meta = alert.get("meta", {})
        sys_event_meta = meta.get("sysEventMeta", {})
        return sys_event_meta.get("instanceName", "")

    @staticmethod
    def _source_bk_obj_id(alarm):
        return alarm.get("bk_obj_id", "")

    @staticmethod
    def _source_bk_inst_id(alarm):
        return alarm.get("bk_inst_id", "")

    @staticmethod
    def _source_meta_info(alarm):
        alert = alarm.get("alert", {})
        meta = alert.get("meta", {})
        sys_event_meta = meta.get("sysEventMeta", {})

        meta_info_parts = []
        if sys_event_meta.get("product"):
            meta_info_parts.append("产品: {}".format(sys_event_meta.get("product")))
        if sys_event_meta.get("regionNameZh"):
            meta_info_parts.append("区域: {}".format(sys_event_meta.get("regionNameZh")))
        if sys_event_meta.get("resourceId"):
            meta_info_parts.append("资源: {}".format(sys_event_meta.get("resourceId")))
        return " | ".join(meta_info_parts) if meta_info_parts else ""

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
        extra["bk_inst_id"] = self._source_bk_inst_id(alarm)
        extra["bk_obj_id"] = self._source_bk_obj_id(alarm)
        extra["item"] = self._source_item(alarm)
        extra["meta_info"] = self._source_meta_info(alarm)
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
        if extra.get("bk_obj_id") and extra.get("bk_inst_id") not in (None, ""):
            event["subject"] = {
                "system": "cmdb",
                "type": str(extra["bk_obj_id"]),
                "id": str(extra["bk_inst_id"]),
                "name": str(extra.get("object") or ""),
            }
        return event
