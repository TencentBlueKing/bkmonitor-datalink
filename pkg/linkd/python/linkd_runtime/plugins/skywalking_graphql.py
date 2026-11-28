# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

# -*- coding: utf-8 -*-
from datetime import datetime, timedelta

import logging
from zoneinfo import ZoneInfo

from linkd_runtime.base import BaseClientV2
from linkd_runtime.errors import TemporaryError

logger = logging.getLogger(__name__)


GRAPHQL_QUERY = """
query queryAlarms($duration: Duration!, $paging: Pagination!) {
    getAlarm(duration: $duration, paging: $paging) {
        msgs {
            id
            name
            message
            startTime
            scope
            tags { key value }
        }
    }
}
"""

ALARM_LEVEL_MAPPING = {
    "critical": "fatal",
    "warning": "warning",
    "info": "remind",
}


class AlarmClient(BaseClientV2):
    ALARM_SOURCE_NEED_CONFIGS = True

    def __init__(self, alarm_source_obj):
        super(AlarmClient, self).__init__(alarm_source_obj)
        self._pull_end_timestamp = None

    def _fetch_source_records(self):
        import requests

        self._source_records = []
        self._pull_end_timestamp = None
        oap_url = self.alarm_source_config_dict.get("oap_url", "").rstrip("/")
        timeline = int(self.alarm_source_config_dict.get("timeline", "1") or "1")

        if not oap_url:
            raise ValueError("缺少必要配置: oap_url")

        if not self.alarm_source_obj.received_at:
            raise ValueError("received_at is required for SkyWalking pull")
        end_time_dt = datetime.fromisoformat(self.alarm_source_obj.received_at.replace("Z", "+00:00")).astimezone(ZoneInfo(self.alarm_source_obj.timezone))
        start_time_dt = end_time_dt - timedelta(hours=timeline)
        if self.alarm_source_obj.meta_info:
            try:
                start_time_dt = datetime.fromtimestamp(int(self.alarm_source_obj.meta_info), ZoneInfo(self.alarm_source_obj.timezone))
            except Exception as error:
                logger.warning("解析 meta_info 中的时间戳失败，使用默认配置时间")

        duration = {
            "start": start_time_dt.strftime("%Y-%m-%d %H%M"),
            "end": end_time_dt.strftime("%Y-%m-%d %H%M"),
            "step": "MINUTE",
        }
        page_num, page_size = 1, 100
        alarms = []
        try:
            while True:
                response = requests.post(
                    "{}/graphql".format(oap_url),
                    json={
                        "query": GRAPHQL_QUERY,
                        "variables": {
                            "duration": duration,
                            "paging": {"pageNum": page_num, "pageSize": page_size},
                        },
                    },
                    timeout=30,
                )
                response.raise_for_status()

                data = response.json()
                if not isinstance(data, dict) or data.get("errors"):
                    raise ValueError("SkyWalking GraphQL 查询失败")
                result = data.get("data")
                if not isinstance(result, dict) or not isinstance(result.get("getAlarm"), dict):
                    raise ValueError("SkyWalking GraphQL 返回无效告警数据")
                msgs = result["getAlarm"].get("msgs")
                if not isinstance(msgs, list):
                    raise ValueError("SkyWalking GraphQL 返回无效告警列表")
                alarms.extend(msgs)
                if len(msgs) < page_size:
                    break
                page_num += 1

            self._source_records = alarms
            self._pull_end_timestamp = int(end_time_dt.timestamp())
            logger.debug("SkyWalking GraphQL 拉取到 %d 条告警", len(self._source_records))
        except (requests.Timeout, requests.ConnectionError) as error:
            raise TemporaryError("SkyWalking request temporarily unavailable") from error

    def proposed_state(self):
        """仅返回本轮窗口终点，宿主在可靠发布后提交，空拉取也可推进。"""
        return {"meta_info": self._pull_end_timestamp}

    @staticmethod
    def _source_event_id(alarm):
        return alarm.get("id", "")

    @staticmethod
    def _source_alarm_time(alarm):
        return alarm.get("startTime")

    @staticmethod
    def _source_item(alarm):
        return alarm.get("scope", "")

    @staticmethod
    def _source_name(alarm):
        scope = alarm.get("scope", "")
        name = alarm.get("name", "")
        if scope and name:
            return "[%s] %s" % (scope, name)
        return name or scope or "未知告警"

    @staticmethod
    def _source_content(alarm):
        return alarm.get("message", "")

    @staticmethod
    def _source_level(alarm):
        tags = alarm.get("tags", [])
        if tags and isinstance(tags, list):
            for tag in tags:
                if isinstance(tag, dict) and tag.get("key") == "level":
                    level_value = tag.get("value", "").lower()
                    return ALARM_LEVEL_MAPPING.get(level_value, "remind")
        return "remind"

    @staticmethod
    def _source_action(alarm):
        return "firing"

    @staticmethod
    def _source_object(alarm):
        return alarm.get("name", "")

    @staticmethod
    def _source_bk_obj_id(alarm):
        return ""

    @staticmethod
    def _source_bk_inst_id(alarm):
        return ""

    @staticmethod
    def _source_meta_info(alarm):
        return ""

    @staticmethod
    def _source_dimension_info(alarm):
        return ""

    @staticmethod
    def get_custom_msg_fields():
        return []

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
        extra["bk_inst_id"] = self._source_bk_inst_id(alarm)
        extra["bk_obj_id"] = self._source_bk_obj_id(alarm)
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
