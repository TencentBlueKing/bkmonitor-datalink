# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

# -*- coding: utf-8 -*-
import hashlib
from datetime import datetime
from zoneinfo import ZoneInfo

from linkd_runtime.base import BaseClientV2
import logging

logger = logging.getLogger(__name__)


ALARM_LEVEL_MAPPING = {
    "Remind": "remind",
    "Warn": "warning",
    "Serious": "fatal",
}


class AlarmClient(BaseClientV2):
    """腾讯云监控告警适配器 Client"""

    ALARM_SOURCE_NEED_CONFIGS = True

    def __init__(self, alarm_source_obj, req_data):
        super(AlarmClient, self).__init__(alarm_source_obj)
        self.data = req_data
        self._source_records = req_data if isinstance(req_data, list) else [req_data]

    @staticmethod
    def _pick_first(*values):
        for value in values:
            if value not in [None, "", [], {}, ()]:
                return value
        return ""

    @staticmethod
    def _stringify(value):
        if value in [None, ""]:
            return ""
        if isinstance(value, (dict, list, tuple)):
            return str(value)
        return str(value)

    @staticmethod
    def _get_nested(data, *path):
        current = data
        for key in path:
            if not isinstance(current, dict):
                return ""
            current = current.get(key)
            if current in [None, ""]:
                return ""
        return current

    def _get_condition(self, alarm):
        conditions = alarm.get("alarmPolicyInfo", {}).get("conditions", [])
        if isinstance(conditions, dict):
            return conditions
        if isinstance(conditions, list) and conditions:
            return conditions[0] if isinstance(conditions[0], dict) else {}
        return {}

    def _normalize_time(self, value):
        if value in [None, "", 0, "0"]:
            raise ValueError("腾讯云告警缺少来源发生时间")

        if isinstance(value, (int, float)):
            timestamp = value / 1000 if value > 1000000000000 else value
            # 保留旧指纹的时间文本，但不能依赖容器本地时区。
            return datetime.fromtimestamp(timestamp, tz=ZoneInfo(self.alarm_source_obj.timezone)).strftime("%Y-%m-%d %H:%M:%S")

        value = str(value).strip()
        if not value:
            raise ValueError("腾讯云告警缺少来源发生时间")

        for fmt in ["%Y-%m-%d %H:%M:%S", "%Y-%m-%d %H:%M", "%Y-%m-%dT%H:%M:%S%z"]:
            try:
                return datetime.strptime(value, fmt).strftime("%Y-%m-%d %H:%M:%S")
            except ValueError:
                pass

        try:
            return datetime.strptime(value.replace("Z", "+0000"), "%Y-%m-%dT%H:%M:%S%z").strftime("%Y-%m-%d %H:%M:%S")
        except ValueError:
            logger.warning("腾讯云告警时间格式无法识别")
            return value

    def _get_dimensions(self, alarm):
        return alarm.get("alarmObjInfo", {}).get("dimensions", {})

    def _source_item(self, alarm):
        condition = self._get_condition(alarm)
        alarm_policy_info = alarm.get("alarmPolicyInfo", {})
        tags = alarm.get("alarmObjInfo", {}).get("tags", {})
        item = self._pick_first(
            condition.get("metricShowName"),
            condition.get("eventShowName"),
            condition.get("eventName"),
            condition.get("metricName"),
            tags.get("alertname"),
            alarm_policy_info.get("policyTypeCName"),
            alarm_policy_info.get("policyType"),
            alarm.get("alarmType", ""),
        )
        return self._stringify(item)

    def _source_name(self, alarm):
        condition = self._get_condition(alarm)
        alarm_policy_info = alarm.get("alarmPolicyInfo", {})
        alarm_obj_info = alarm.get("alarmObjInfo", {})

        policy_name = alarm_policy_info.get("policyName", "")
        event_name = self._pick_first(condition.get("eventShowName"), condition.get("eventName"))
        metric_name = self._pick_first(condition.get("metricShowName"), condition.get("metricName"))
        prometheus_name = alarm_obj_info.get("summary", "")

        if policy_name and event_name:
            return "{} - {}".format(policy_name, event_name)

        return self._stringify(
            self._pick_first(
                policy_name, event_name, metric_name, prometheus_name, alarm.get("alarmType", "腾讯云告警")
            )
        )

    def _source_event_id(self, alarm):
        event_id = self._pick_first(alarm.get("sessionId"), alarm.get("alertId"))
        if event_id:
            return self._stringify(event_id)

        condition = self._get_condition(alarm)
        unique_str = "{}:{}:{}:{}:{}".format(
            self.alarm_source_id,
            self._source_object(alarm),
            self._pick_first(condition.get("metricName"), condition.get("eventName"), alarm.get("alarmType", "")),
            self._source_level(alarm),
            self._normalize_time(self._source_alarm_time(alarm)),
        )
        return hashlib.md5(unique_str.encode("utf8")).hexdigest()

    @classmethod
    def _source_alarm_time(cls, alarm):
        return cls._pick_first(
            alarm.get("firstOccurTime"), alarm.get("alarmTime"), cls._get_nested(alarm, "alarmObjInfo", "timestamp")
        )

    def _source_content(self, alarm):
        condition = self._get_condition(alarm)
        alarm_obj_info = alarm.get("alarmObjInfo", {})

        if alarm.get("alarmType") == "prometheus":
            return self._stringify(
                self._pick_first(
                    alarm_obj_info.get("content"), alarm_obj_info.get("summary"), alarm_obj_info.get("tags", {})
                )
            )

        if condition.get("eventShowName") or condition.get("eventName"):
            event_show_name = self._pick_first(condition.get("eventShowName"), condition.get("eventName"))
            product_show_name = self._pick_first(condition.get("productShowName"), condition.get("productName"))
            return "{} - {}".format(product_show_name, event_show_name).strip(" -")

        metric_show_name = self._pick_first(condition.get("metricShowName"), condition.get("metricName"))
        calc_type = condition.get("calcType", "")
        calc_value = condition.get("calcValue", "")
        current_value = condition.get("currentValue", "")
        unit = condition.get("unit", "")
        operation = alarm_obj_info.get("operation", "")

        content_parts = []
        if metric_show_name:
            content_parts.append(metric_show_name)
        if calc_type and calc_value:
            content_parts.append("{} {}".format(calc_type, calc_value))
        if current_value:
            content_parts.append("当前值为 {}{}".format(current_value, unit))
        if operation:
            content_parts.append("接口: {}".format(operation))

        return self._stringify("，".join(content_parts) if content_parts else self._source_name(alarm))

    @staticmethod
    def _source_action(alarm):
        alarm_status = str(alarm.get("alarmStatus", 1))
        if alarm_status == "1":
            return "firing"
        if alarm_status == "0":
            return "resolved"
        if alarm_status == "5":
            return "close"
        return "firing"

    @staticmethod
    def _source_level(alarm):
        alarm_level = alarm.get("alarmLevel")
        if alarm_level:
            return ALARM_LEVEL_MAPPING.get(alarm_level, "remind")

        rum_level = AlarmClient._get_nested(alarm, "alarmObjInfo", "level")
        if rum_level:
            try:
                rum_level = int(rum_level)
                if rum_level >= 4:
                    return "fatal"
                if rum_level >= 2:
                    return "warning"
            except (TypeError, ValueError):
                pass
        return "remind"

    def _source_object(self, alarm):
        alarm_obj_info = alarm.get("alarmObjInfo", {})
        dimensions = self._get_dimensions(alarm)
        tags = alarm_obj_info.get("tags", {})
        return self._stringify(
            self._pick_first(
                dimensions.get("objName"),
                dimensions.get("unInstanceId"),
                dimensions.get("instanceId"),
                dimensions.get("objId"),
                alarm_obj_info.get("service.name"),
                alarm_obj_info.get("instanceId"),
                alarm_obj_info.get("tapm.instance.key"),
                tags.get("tcloud_prometheus_instance_id"),
                tags.get("alertname"),
                alarm_obj_info,
            )
        )

    @staticmethod
    def _source_bk_biz_id(alarm):
        return alarm.get("bk_biz_id", "")

    @staticmethod
    def _source_bk_biz_name(alarm):
        return alarm.get("bk_biz_name", "")

    @staticmethod
    def _source_bk_set_id(alarm):
        return alarm.get("bk_set_id", "")

    @staticmethod
    def _source_bk_set_name(alarm):
        return alarm.get("bk_set_name", "")

    @staticmethod
    def _source_bk_module_id(alarm):
        return alarm.get("bk_module_id", "")

    @staticmethod
    def _source_bk_module_name(alarm):
        return alarm.get("bk_module_name", "")

    @staticmethod
    def _source_bk_obj_id(alarm):
        return alarm.get("bk_obj_id", "")

    @staticmethod
    def _source_bk_inst_id(alarm):
        return alarm.get("bk_inst_id", "")

    def _source_meta_info(self, alarm):
        alarm_obj_info = alarm.get("alarmObjInfo", {})
        alarm_policy_info = alarm.get("alarmPolicyInfo", {})
        condition = self._get_condition(alarm)
        meta_parts = []
        for label, value in [
            ("告警类型", alarm.get("alarmType", "")),
            ("策略类型", alarm_policy_info.get("policyTypeCName") or alarm_policy_info.get("policyType", "")),
            ("产品", condition.get("productShowName") or condition.get("productName", "")),
            ("命名空间", alarm_obj_info.get("namespace", "")),
            ("地域", alarm_obj_info.get("region", "")),
        ]:
            if value:
                meta_parts.append("{}: {}".format(label, value))
        return " | ".join(meta_parts)

    def _source_dimension_info(self, alarm):
        alarm_obj_info = alarm.get("alarmObjInfo", {})
        return self._stringify(self._pick_first(alarm_obj_info.get("dimensions"), alarm_obj_info.get("tags"), ""))

    @staticmethod
    def get_custom_msg_fields():
        return []

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
        extra["bk_biz_id"] = self._source_bk_biz_id(alarm)
        extra["bk_biz_name"] = self._source_bk_biz_name(alarm)
        extra["bk_inst_id"] = self._source_bk_inst_id(alarm)
        extra["bk_module_id"] = self._source_bk_module_id(alarm)
        extra["bk_module_name"] = self._source_bk_module_name(alarm)
        extra["bk_obj_id"] = self._source_bk_obj_id(alarm)
        extra["bk_set_id"] = self._source_bk_set_id(alarm)
        extra["bk_set_name"] = self._source_bk_set_name(alarm)
        extra["dimension_info"] = self._source_dimension_info(alarm)
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
            "dimensions": dict(self._get_dimensions(alarm) or {}),
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
