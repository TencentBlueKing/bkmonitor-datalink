# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

import hashlib
import re


from linkd_runtime.base import BaseClientV2


LEVEL_DICT = {
    2: 'fatal',
    1: 'warning',
}

ALARM_TYPE_DICT = {
    3: "应用",
    22: "关键事务",
    57: "外部服务",
}

METRIC_DICT = {
    21: "Apdex",
    22: "应用响应时间",
    23: "错误率",
    84: "Heap使用率",
    85: "线程数",
    112: "无数据-吞吐率监测",
    111: "无数据-心跳监测",
    51: "vip-应用响应时间",
    52: "vip-错误",
}

IP_MATCH_PATTEN = re.compile(r"\d+\.\d+\.\d+\.\d+")


class AlarmClient(BaseClientV2):
    """
    获取听云告警信息
    """

    # 定义告警源获取告警是否需要额外配置信息
    ALARM_SOURCE_NEED_CONFIGS = True
    # 定义是否通过ip拿到主机在cmdb中的信息

    def __init__(self, alarm_source_obj):
        super().__init__(alarm_source_obj)

    def _fetch_source_records(self):
        self._source_records = self.send_get_request()

    def send_get_request(self):
        import requests
        resp = requests.get(
            timeout=30,
            url="http://{}/server/data/application/allApps?X-Auth-Key={}&status=1".format(
                self.alarm_source_config_dict.get("host"), self.alarm_source_config_dict.get("auth_key")
            )
        )
        res_alarm_list = []  # 活动告警
        if resp.status_code == 200:
            data = resp.json()
            app_id_list = [app_info["id"] for app_info in data["appList"]]
            for app_id in app_id_list:
                for alarm_type in ALARM_TYPE_DICT:
                    res_alarm = requests.get(
                        timeout=30,
                        url="http://{0}/server/data/event/list?targetId={2}&eventType=0&type={3}&X-Auth-Key={1}".format(
                            self.alarm_source_config_dict.get("host"),
                            self.alarm_source_config_dict.get("auth_key"),
                            app_id,
                            alarm_type,
                        )
                    )
                    if res_alarm.status_code != 200:
                        # 任一应用/类型失败都使整批失败，不能把不完整快照当成成功。
                        # 不附带 URL 或正文，避免泄漏查询参数中的认证信息。
                        raise RuntimeError("pull alarm subrequest error, status_code: %s" % res_alarm.status_code)
                    alarm_data = res_alarm.json()
                    for key, value in alarm_data.items():
                        res_alarm_list += [info for info in value if info["status"] == 1]
        else:
            raise RuntimeError("pull alarm request error, status_code: %s" % resp.status_code)
        return res_alarm_list

    @staticmethod
    def _source_object(alarm):
        ips = IP_MATCH_PATTEN.findall(alarm["instanceName"])
        return ips[0] if ips else ""

    @staticmethod
    def _source_item(alarm):
        return METRIC_DICT.get(alarm["metricId"], "")

    @staticmethod
    def _source_name(alarm):
        return "应用'{}'实例'{}'发生{}告警".format(
            alarm["targetName"], alarm["instanceName"], alarm.get("alarm_type", "")
        )

    def _source_event_id(self, alarm):
        return hashlib.md5(
            "{}:{}:{}".format(self.alarm_source_id[:8], str(alarm["id"]), alarm["eventLevel"]).encode("utf8")
        ).hexdigest()

    @staticmethod
    def _source_alarm_time(alarm):
        return alarm["beginTimeMills"]

    @staticmethod
    def _source_content(alarm):
        return "{}发生告警，当前值为{}".format(alarm["instanceName"], alarm["value"])

    @staticmethod
    def _source_level(alarm):
        return LEVEL_DICT[alarm["eventLevel"]]

    def pull_raw_events(self):
        self._fetch_source_records()
        return [self._standard_event(alarm) for alarm in self._source_records]

    def _standard_event(self, alarm):
        # 1. 第三方来源身份直接用于关联，发生时间和事件编号不替代 fingerprint。
        fingerprint = self._source_event_id(alarm)
        if fingerprint is None or fingerprint == "":
            raise ValueError("来源缺少稳定告警身份")
        action = alarm.get("action", "firing")
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
