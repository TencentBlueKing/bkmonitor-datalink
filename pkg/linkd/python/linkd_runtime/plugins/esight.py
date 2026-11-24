# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""eSight 来源采集；HTTP 失败不返回部分分页结果。"""
from datetime import datetime

from linkd_runtime.base import BaseClientV2
from linkd_runtime.errors import TemporaryError

ALARM_LEVEL_MAPPING = {
    1: "fatal",
    2: "warning",
    3: "warning",
    4: "remind",
    0: "remind",
}


class AlarmClient(BaseClientV2):
    ALARM_SOURCE_NEED_CONFIGS = True

    def __init__(self, alarm_source_obj):
        super(AlarmClient, self).__init__(alarm_source_obj)

    def _fetch_source_records(self):
        if not self.alarm_source_config_dict:
            raise ValueError("未进行告警源配置")
        host = self.alarm_source_config_dict.get("host", "")
        userid = self.alarm_source_config_dict.get("userid", "")
        value = self.alarm_source_config_dict.get("value", "")
        ipaddr = self.alarm_source_config_dict.get("ipaddr", "")
        collect_cycle = self.alarm_source_obj.celery_task_time_interval
        openid_result = get_openid(host, userid, value, ipaddr)
        if openid_result["result"]:
            openid = openid_result["message"]
            alarm_result = get_alarm(host, openid, collect_cycle, self.alarm_source_obj.received_at)
            if alarm_result["result"]:
                self._source_records = alarm_result["message"]
            else:
                raise Exception(alarm_result["message"])
        else:
            raise Exception(openid_result["message"])

    @staticmethod
    def _source_item(alarm):
        return str(alarm.get("eventType", ""))

    @staticmethod
    def _source_name(alarm):
        return alarm.get("alarmName", "")

    @staticmethod
    def _source_event_id(alarm):
        return alarm.get("moName") + "-" + str(alarm.get("eventType")) + "-" + str(alarm.get("perceivedSeverity"))

    @staticmethod
    def _source_alarm_time(alarm):
        return alarm["latestLogTime"]

    @staticmethod
    def _source_content(alarm):
        object_instance = alarm.get("objectInstance") or ""
        additional_information = alarm.get("additionalInformation") or ""
        return object_instance + "-" + additional_information

    @staticmethod
    def _source_action(alarm):
        return "resolved" if alarm.get("cleared") else "firing"

    @staticmethod
    def _source_level(alarm):
        return ALARM_LEVEL_MAPPING.get(alarm.get("perceivedSeverity"), "remind")

    @staticmethod
    def _source_object(alarm):
        return alarm.get("moName", "")

    @staticmethod
    def _source_bk_obj_id(alarm):
        return alarm.get("bk_obj_id", "")

    @staticmethod
    def _source_bk_inst_id(alarm):
        return alarm.get("bk_inst_id", "")

    @staticmethod
    def _source_meta_info(alarm):
        return alarm.get("meta_info", "")

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


def _request(method, url, **kwargs):
    import requests

    try:
        response = getattr(requests, method)(url=url, timeout=30, verify=False, **kwargs)
        response.raise_for_status()
        body = response.json()
        if body.get("code") != 0:
            raise ValueError("eSight请求被拒绝，请检查连接配置或服务状态")
        return body
    except (requests.Timeout, requests.ConnectionError) as error:
        raise TemporaryError("eSight temporarily unavailable") from error


def get_openid(host, userid, value, ipaddr):
    body = {"userid": userid, "value": value}
    if ipaddr:
        body["ipaddr"] = ipaddr
    result = _request("put", f"https://{host}/rest/openapi/sm/session", headers={"Content-Type": "application/x-www-form-urlencoded"}, data=body)
    return {"result": True, "message": result["data"]}


def get_alarm(host, openid, collect_cycle, received_at):
    # 宿主固定本轮窗口，分页与重试均不得扩大到新的处理时间。
    end = int(datetime.fromisoformat(received_at.replace("Z", "+00:00")).timestamp())
    params = {"startTime": (end - (collect_cycle + 500) * 60) * 1000, "endTime": end * 1000, "pageSize": 100, "pageNo": 1}
    headers = {"Content-Type": "application/json", "openid": openid}
    url = f"https://{host}/rest/openapi/alarm"
    first = _request("get", url, headers=headers, params=dict(params))
    alarms = list(first["data"])
    for page in range(2, first["totalPage"] + 1):
        params["pageNo"] = page
        alarms.extend(_request("get", url, headers=headers, params=dict(params))["data"])
    return {"result": True, "message": alarms}
