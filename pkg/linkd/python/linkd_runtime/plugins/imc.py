# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

# -*- coding: utf-8 -*-
import logging

from linkd_runtime.base import BaseClientV2
from linkd_runtime.errors import TemporaryError
logger = logging.getLogger(__name__)


ALARM_LEVEL_MAPPING = {
    "1": "fatal",
    "2": "warning",
    "3": "remind",
    "4": "remind",
    "5": "remind",
}


class AlarmClient(BaseClientV2):
    ALARM_SOURCE_NEED_CONFIGS = True

    def __init__(self, alarm_source_obj):
        super(AlarmClient, self).__init__(alarm_source_obj)
        self.host = self.alarm_source_config_dict.get("host", "").rstrip("/")
        self.user = self.alarm_source_config_dict.get("user", "")
        self.password = self.alarm_source_config_dict.get("password", "")
        self.time_range = self.alarm_source_config_dict.get("time_range", "7")

    def _fetch_source_records(self):
        if not all([self.host, self.user, self.password]):
            raise ValueError("IMC地址/账号/密码未配置完整！")

        params = {
            "operatorName": self.user,
            "recStatus": "0",
            "timeRange": self.time_range,
            "size": "10000",
            "desc": "false",
            "faultOidFuzzyQuery": "false",
        }

        resp_data = self._imc_api_get("/imcrs/fault/alarm", params)
        remote_alarm = resp_data.get("alarm") or []

        if isinstance(remote_alarm, list):
            alarm_list = remote_alarm
        else:
            alarm_list = [remote_alarm]

        event_ids = [alarm.get("id") for alarm in alarm_list]
        active_alarms = self.query_alarm_source_not_close_alarm_by_event_id(event_ids) if event_ids else []
        active_event_ids = [alarm.get("event_id") for alarm in active_alarms]
        self._source_records = self._process_alarm_list(alarm_list, active_event_ids)

        try:
            active_alarms = self.query_alarm_source_not_close_alarm(self.alarm_source_obj)
            if not active_alarms:
                return

            params = {
                "operatorName": self.user,
                "recStatus": "1",
                "timeRange": self.time_range,
                "size": "10000",
                "desc": "false",
                "faultOidFuzzyQuery": "false",
            }
            resp_data = self._imc_api_get("/imcrs/fault/alarm", params)
            remote_alarm_resolved = resp_data.get("alarm", [])

            if not remote_alarm_resolved:
                return

            if isinstance(remote_alarm_resolved, list):
                remote_alarm_list = remote_alarm_resolved
            else:
                remote_alarm_list = [remote_alarm_resolved]

            resolved_event_ids = [remote_alarm.get("id") for remote_alarm in remote_alarm_list]
            alarms_to_resolve = [alarm for alarm in active_alarms if alarm.get("event_id", "") in resolved_event_ids]

            if alarms_to_resolve:
                self.match_alarm_resolved(alarms_to_resolve)
        except Exception:
            # 恢复失败必须由宿主看到，不能返回伪装完整成功的采集批次。
            raise

    def _process_alarm_list(self, raw_alarms, active_event_ids):
        processed = []
        for alarm in raw_alarms:
            if alarm.get("id") in active_event_ids:
                continue
            device_name = alarm.get("deviceName", "")
            device_ip = alarm.get("deviceIp", "")
            alarm_object = "{} {}".format(device_name, device_ip).strip()

            original_type_desc = alarm.get("originalTypeDesc", "")
            if original_type_desc == "The System":
                alarm_item = "The System 获取"
            elif original_type_desc == "Trap":
                alarm_item = "Trap 设备推送"
            else:
                alarm_item = original_type_desc

            alarm_obj = {
                "id": alarm.get("id", ""),
                "event_id": alarm.get("id", ""),
                "deviceName": device_name,
                "deviceIp": device_ip,
                "alarmLevel": alarm.get("alarmLevel", "5"),
                "alarmLevelDesc": alarm.get("alarmLevelDesc", ""),
                "alarmCategoryDesc": alarm.get("alarmCategoryDesc", ""),
                "faultTime": alarm.get("faultTime", ""),
                "faultTimeDesc": alarm.get("faultTimeDesc", ""),
                "alarmDesc": alarm.get("alarmDesc", ""),
                "originalTypeDesc": original_type_desc,
                "recStatus": alarm.get("recStatus", "0"),
                "object": alarm_object,
                "item": alarm_item,
            }
            processed.append(alarm_obj)
        return processed

    def _imc_api_get(self, path, params):
        import requests
        from requests.auth import HTTPDigestAuth

        try:
            response = requests.get(
                url=self.host + path, params=params,
                headers={"Accept": "application/json", "Content-Type": "application/json"},
                auth=HTTPDigestAuth(self.user, self.password), verify=False, timeout=20,
            )
            response.raise_for_status()
            return response.json()
        except (requests.Timeout, requests.ConnectionError) as error:
            raise TemporaryError("IMC request temporarily unavailable") from error

    @staticmethod
    def _source_item(alarm):
        return alarm.get("item", "")

    @staticmethod
    def _source_name(alarm):
        return alarm.get("alarmCategoryDesc", "IMC告警")

    @staticmethod
    def _source_event_id(alarm):
        return alarm.get("event_id", "")

    @staticmethod
    def _source_alarm_time(alarm):
        return alarm.get("faultTimeDesc", "")

    @staticmethod
    def _source_content(alarm):
        return alarm.get("alarmDesc", "")

    @staticmethod
    def _source_action(alarm):
        rec = str(alarm.get("recStatus", "0"))
        return "firing" if rec == "0" else "resolved"

    @staticmethod
    def _source_level(alarm):
        level = str(alarm.get("alarmLevel", "5")).strip()
        return ALARM_LEVEL_MAPPING.get(level, "remind")

    @staticmethod
    def _source_object(alarm):
        return alarm.get("object", "")

    @staticmethod
    def _source_meta_info(alarm):
        return alarm.get("id", "")

    def get_metric(self, alarm, start_time, end_time):
        return {}

    def pull_raw_events(self):
        self._fetch_source_records()
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
        if extra.get("object"):
            event["subject"] = {"name": str(extra["object"])}
        return event
