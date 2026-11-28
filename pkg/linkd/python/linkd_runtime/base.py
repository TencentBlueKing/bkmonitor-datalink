# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""插件计算边界；执行结果不表示发布成功，运行库不持有存储连接。"""

import logging
from dataclasses import dataclass, field
from datetime import datetime
from zoneinfo import ZoneInfo

from .raw_event import validate_raw_event


@dataclass
class Source:
    """宿主固定的一次执行来源快照；配置由宿主完成解密。"""

    id: str
    name: str
    bk_tenant_id: str
    configs: dict = field(default_factory=dict)
    timezone: str = "Asia/Shanghai"
    meta_info: str | int | None = None
    activate_time: str | None = None
    received_at: str | None = None
    active_alerts: object = None
    metrics: object = None
    celery_task_time_interval: int = 0
    plugin_source_id: str = ""

    def get_config_dict(self):
        return dict(self.configs)


def source_time(value, timezone="Asia/Shanghai"):
    """沿用 KAC 时间戳单位判断；无时区来源时间按显式时区解释。"""
    zone = ZoneInfo(timezone)
    if isinstance(value, (int, float)):
        value = datetime.fromtimestamp(
            value / 1000 if value > 100000000000 else value, tz=zone
        )
    elif isinstance(value, str):
        value = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if value.tzinfo is None:
        value = value.replace(tzinfo=zone)
    return value.isoformat()


class BaseClientV2:
    """只提供插件计算上下文；Kafka 发布和进度提交属于 Linkd 宿主。"""

    def __init__(self, source):
        self.alarm_source_obj = source
        # 原插件可能把 KAC 来源 ID 纳入 fingerprint；查询作用域仍是 source.id。
        self.alarm_source_id = source.plugin_source_id or source.id
        self.alarm_source_config_dict = source.get_config_dict()
        self.logger = logging.getLogger(__name__)
        self._source_records = []
        self._terminal_events = []

    def pull_raw_events(self):
        raise NotImplementedError

    @staticmethod
    def get_custom_msg_fields():
        """沿用 KAC 基类默认值；没有扩展消息字段的插件返回空列表。"""
        return []

    def get_metric(self, alarm, start_time, end_time):
        """沿用 KAC 的未实现指标查询返回值，不代表上游查询成功。"""
        return {}

    def proposed_state(self):
        """返回候选运行进度；None 表示本轮不修改持久化进度。"""
        return None

    def query_alarm_source_not_close_alarm(self, source, query=None):
        """宿主按可信租户和来源查询活动告警；缺少能力必须失败，不能假装无告警。"""
        if source.active_alerts is None:
            raise RuntimeError("active alert reader is not configured")
        return source.active_alerts(source.bk_tenant_id, source.id, query or {})

    def query_latest_source_alarm(self):
        source = self.alarm_source_obj
        if source.active_alerts is None:
            raise RuntimeError("source alert reader is not configured")
        return source.active_alerts.latest(source.bk_tenant_id, source.id)

    def query_alarm_source_not_close_alarm_by_event_id(self, event_ids):
        return self.query_alarm_source_not_close_alarm(self.alarm_source_obj, {"event_id": event_ids})

    def match_alarm_resolved(self, alarms):
        """使用宿主固定接收时间生成恢复事件，重试不能重新取当前时间。"""
        source = self.alarm_source_obj
        if not source.received_at:
            raise ValueError("received_at is required for recovery")
        for alarm in alarms:
            if alarm.get("bk_tenant_id") != source.bk_tenant_id or alarm.get("source_id") != source.id:
                raise ValueError("recovery alert scope mismatch")
            self._terminal_events.append({
                "fingerprint": alarm["fingerprint"],
                "title": alarm.get("title") or alarm.get("name") or "来源告警恢复",
                "evaluations": [{"action": "resolved", "severity": alarm["level"]}],
                "occurred_at": source.received_at,
                "dimensions": alarm.get("dimensions", {}),
                "labels": alarm.get("labels", {}),
                "subject": alarm.get("subject", {}),
                "extra_data": alarm.get("extra_data", {}),
            })

    def source_time(self, value):
        """按本次来源配置解释时间，避免进程之间共享可变时区。"""
        return source_time(value, self.alarm_source_obj.timezone)

    def pull_recovery_events(self):
        raise NotImplementedError("该插件未实现独立恢复入口")

    def execute(self, *, recovery=False):
        """返回完整校验后的批次；失败时不返回部分结果，不执行发布副作用。"""
        self._terminal_events = []
        events = list(self.pull_recovery_events() if recovery else self.pull_raw_events())
        # 普通拉取也可能产生恢复事件；先完整计算，再统一返回，避免遗漏恢复。
        if not recovery:
            events.extend(self._terminal_events)
        result = []
        for event in events:
            if isinstance(event, dict):
                if event.get("action") == "heartbeat":
                    continue
                event = {**event, "bk_tenant_id": self.alarm_source_obj.bk_tenant_id}
                evaluations = event.get("evaluations")
                if isinstance(evaluations, list):
                    remaining = [item for item in evaluations if not isinstance(item, dict) or item.get("action") != "heartbeat"]
                    if len(remaining) != len(evaluations):
                        if not remaining:
                            continue
                        event = {**event, "evaluations": remaining}
            validate_raw_event(event, tenant_id=self.alarm_source_obj.bk_tenant_id)
            if recovery and any(item["action"] not in {"resolved", "closed"} for item in event["evaluations"]):
                raise ValueError("独立恢复入口只能生成 resolved/closed 事件")
            result.append(event)
        return result
