# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

import datetime
import ssl

import hashlib

def get_event_id_by_content(content):
    """保留既有 vCenter 告警关联算法，不引入新的校验用途。"""
    return hashlib.md5(content.encode("utf-8")).hexdigest()

from linkd_runtime.base import BaseClientV2


class AlarmClient(BaseClientV2):
    """
    获取蓝鲸监控告警信息
    """

    # 定义告警源获取告警是否需要额外配置信息
    ALARM_SOURCE_NEED_CONFIGS = True
    # 定义是否通过ip拿到主机在cmdb中的信息

    def __init__(self, alarm_source_obj):
        super().__init__(alarm_source_obj)

    def _fetch_source_records(self):
        alarm_list = self.get_vcenter_alarms()

        # 通过id去重, 避免出现相同告警, 程序报错
        id_alarm_dict = {id(x): x for x in alarm_list}
        self._source_records = list(id_alarm_dict.values())

    def get_vcenter_alarms(self):
        from pyVim.connect import Disconnect

        connect = self.connect_vc()
        try:
            content = connect.content
            alarms = content.alarmManager.GetAlarm()
            reader = VcenterAlarmsApi(self.source_time)
            minutes = self.alarm_source_obj.celery_task_time_interval + 10
            end = datetime.datetime.fromisoformat(self.alarm_source_obj.received_at.replace("Z", "+00:00"))
            reader.get_alarms_all(content, minutes, alarms, end)
            return self.vcenter_alarms_format(reader.alarms_all)
        finally:
            Disconnect(connect)

    # 连接VC服务
    def connect_vc(self):
        """连接VC服务"""
        from pyVim.connect import SmartConnect

        vcserver_config = self.alarm_source_config_dict
        # 沿用原插件不校验证书的策略，但让 OpenSSL 协商可用 TLS 版本。
        # 强制 TLS 1.0 与当前默认密码套件不兼容，不能依赖 SSLEOFError 才回退。
        context = ssl._create_unverified_context()
        try:
            return SmartConnect(
                host=vcserver_config["ip"],
                user=vcserver_config["user"],
                pwd=vcserver_config["password"],
                port=443,
                sslContext=context,
                httpConnectionTimeout=30,
                connectionPoolTimeout=60,
            )
        except Exception as error:
            raise RuntimeError("connect vcenter failed") from error

    @staticmethod
    def vcenter_alarms_format(vcenter_alarms):
        # 保留实体与告警定义的最新事实，不先构造旧 KAC 告警结构。
        latest = {}
        for item in vcenter_alarms:
            if "Virtual machine" in item["alarm"]:
                continue
            identity = (item["entity_name"], item["alarm"])
            if identity not in latest or item["createdTime"] > latest[identity]["createdTime"]:
                latest[identity] = item
        return list(latest.values())

    def pull_raw_events(self):
        self._fetch_source_records()
        return [self._standard_event(alarm) for alarm in self._source_records]

    def _standard_event(self, alarm):
        message = alarm["fullFormattedMessage"]
        recovered = "to Green" in message
        state = "to Green" if recovered else "to Yellow" if "to Yellow" in message else "to Red"
        return {
            "fingerprint": get_event_id_by_content(alarm["entity_name"] + alarm["alarm"]),
            "title": alarm["alarm"],
            "content": message,
            "evaluations": [{"action": "resolved" if recovered else "triggered", "severity": "warning"}],
            "occurred_at": self.source_time(alarm["createdTime"]),
            "dimensions": {"entity": alarm["entity_name"]},
            "subject": {"name": alarm["entity_name"]},
            "extra_data": {**alarm, "item": alarm["alarm"], "meta_info": state},
        }


class VcenterAlarmsApi:
    """按来源返回顺序读取事件；并发由执行器预算统一控制。"""

    def __init__(self, format_time):
        self.alarms_all = []
        self.format_time = format_time

    def get_alarms_all(self, content, minutes, alarms, end):
        from pyVmomi import vim

        for alarm in alarms:
            event_filter = vim.event.EventFilterSpec()
            event_filter.time = vim.EventFilterSpecByTime()
            event_filter.time.beginTime = end - datetime.timedelta(minutes=minutes)
            event_filter.time.endTime = end
            event_filter.alarm = alarm
            collector = content.eventManager.CreateCollectorForEvents(event_filter)
            try:
                while True:
                    events = collector.ReadNextEvents(500)
                    if not events:
                        break
                    for event in events:
                        self.alarms_all.append({
                            "createdTime": self.format_time(event.createdTime),
                            "userName": event.userName,
                            "fullFormattedMessage": getattr(event, "fullFormattedMessage", ""),
                            "entity_name": event.entity.name if hasattr(event, "entity") else "",
                            "alarm": event.alarm.name,
                        })
            finally:
                collector.DestroyCollector()
