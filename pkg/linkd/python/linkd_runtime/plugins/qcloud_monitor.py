# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

import base64
import copy
import datetime
import hashlib
import hmac
import json
import math
import random
import time
from zoneinfo import ZoneInfo

from linkd_runtime.base import BaseClientV2


class AlarmClient(BaseClientV2):
    """
    获取腾讯云监控告警信息
    """

    # 定义告警源获取告警是否需要额外配置信息
    ALARM_SOURCE_NEED_CONFIGS = True

    def __init__(self, alarm_source_obj):
        super().__init__(alarm_source_obj)
        self.now = datetime.datetime.fromisoformat(alarm_source_obj.received_at.replace("Z", "+00:00"))
        self.pulled = False

    def _fetch_source_records(self):
        self._source_records = self.send_get_request()
        self.pulled = True

    def send_get_request(self):
        now = self.now
        # 腾讯云接口时效性存在3分钟左右误差
        qcloud_monitor_delay_time = 3
        start_time_datetime = (datetime.datetime.fromisoformat(self.alarm_source_obj.activate_time) if self.alarm_source_obj.activate_time else now) - datetime.timedelta(
            minutes=(self.alarm_source_obj.celery_task_time_interval + qcloud_monitor_delay_time)
        )

        # 判断一下开始时间, 防止两次开启时间间隔太长, 一次拉取太多数据
        if start_time_datetime < now - datetime.timedelta(hours=3):
            start_time_datetime = now - datetime.timedelta(hours=3)

        start_time = int(start_time_datetime.timestamp())
        end_time = int(now.timestamp())
        request_params = {"start_time": start_time, "end_time": end_time}

        # 获取regions数据
        qcloud_api = QCloudApi(qcloud_config=self.alarm_source_config_dict)
        result = qcloud_api.get_basic_alarms(request_params["start_time"], request_params["end_time"])
        if result["result"]:
            return result.get("alarm_list", [])
        else:
            raise RuntimeError(result.get("message", ""))

    @staticmethod
    def _source_item(alarm):
        alarm_type_origin = alarm["AlarmType"]  # 告警类型，0表示指标告警，2表示产品事件告警，3表示平台事件告警
        if alarm_type_origin == 0:
            alarm_type = alarm["MetricName"]
        else:
            alarm_type = alarm.get("Content", "")
        return alarm_type

    @staticmethod
    def _get_ip(alarm):
        # ObjName示例数据：192.0.2.1(example-host)
        return alarm.get("ObjName").split("(")[0] if alarm.get("ObjName") else ""

    @staticmethod
    def _time_format(time_str):
        # time_str示例数据: u'2020-02-18T11:33:00+08:00'
        return time_str.split("+")[0].replace("T", " ")

    @staticmethod
    def _source_name(alarm):
        return alarm.get("Content", "")

    def _source_alarm_time(self, alarm):
        return alarm["FirstOccurTime"]

    def _source_content(self, alarm):
        content = alarm.get("Content", "")
        ip = self._get_ip(alarm)
        if content and ip:
            content = "主机[{}]：{}".format(ip, content)
        return content

    @staticmethod
    def _source_event_id(alarm):
        return str(alarm.get("Id", ""))

    @staticmethod
    def _source_level(alarm):
        # 此处告警源中无告警等级指标，兼容APP全部设置为warning
        return alarm.get("level", "warning")

    def _source_object(self, alarm):
        return self._get_ip(alarm)

    @staticmethod
    def _source_meta_info(alarm):
        """将监控事件id存盘, 用来告警自恢复"""
        return str(alarm.get("Id", ""))

    # @staticmethod
    # def _source_expansion_field_1(alarm):
    #     return alarm.get("ProjectName", "")

    def _fetch_recovery(self):
        alarm_source_obj = self.alarm_source_obj
        """更新腾讯云监控告警的状态"""
        active_alarms = self.query_alarm_source_not_close_alarm(alarm_source_obj)

        qcloud_alarm_id_alarm_dict = {i["meta_info"]: i for i in active_alarms if i["meta_info"]}
        qcloud_alarm_id_list = qcloud_alarm_id_alarm_dict.keys()
        if not qcloud_alarm_id_list:
            return

        qcloud_config = alarm_source_obj.get_config_dict()
        if not qcloud_config:
            raise RuntimeError("未进行告警源配置!")

        qcloud_api = QCloudApi(qcloud_config=qcloud_config)

        times = [datetime.datetime.strptime(alarm["alarm_time"], "%Y-%m-%d %H:%M:%S").replace(tzinfo=ZoneInfo(self.alarm_source_obj.timezone)).timestamp() for alarm in active_alarms]
        start_time, end_time = int(min(times)), int(max(times))

        # 取已恢复的告警
        alarm_status = {
            "AlarmStatus.0": 1,
            "AlarmStatus.1": 2,
            "AlarmStatus.2": 3,
            "AlarmStatus.3": 4,
            "AlarmStatus.4": 5,
        }
        result = qcloud_api.get_basic_alarms(start_time, end_time, **alarm_status)
        if result["result"]:
            recovered_alarm_list = []
            for qcloud_alarm in result["alarm_list"]:
                alarm_id = str(qcloud_alarm["Id"])
                if alarm_id in qcloud_alarm_id_list:
                    recovered_alarm_list.append(qcloud_alarm_id_alarm_dict[alarm_id])
            if recovered_alarm_list:
                self.match_alarm_resolved(recovered_alarm_list)
        else:
            raise RuntimeError("QCloudMonitor alarm resolved error")

    def proposed_state(self):
        return {"activate_time": self.now.isoformat()} if self.pulled else None

    def pull_raw_events(self):
        self._fetch_source_records()
        return [self._standard_event(alarm) for alarm in self._source_records]

    def pull_recovery_events(self):
        self._terminal_events = []
        self._fetch_recovery()
        return self._terminal_events

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


class QCloudApi:
    """
    调用腾讯云API接口方法类，包含：获取基础告警列表，获取地域列表
    """

    def __init__(self, qcloud_config):
        self.secret_id = qcloud_config.get("secret_id", "")
        self.secret_key = qcloud_config.get("secret_key", "")

    @staticmethod
    def _get_string_to_sign(method, endpoint, params):
        sign_str = method + endpoint + "/?"
        query_str = "&".join("{}={}".format(k, params[k]) for k in sorted(params))
        return sign_str + query_str

    @staticmethod
    def _sign_str(key, s, method):
        hmac_str = hmac.new(key.encode("utf8"), s.encode("utf8"), method).digest()
        return base64.b64encode(hmac_str).decode("utf8")

    def _append_signature(self, endpoint, api_params):
        s = self._get_string_to_sign("GET", endpoint, api_params)
        return self._sign_str(self.secret_key, s, hashlib.sha1)

    def send_request(self, endpoint, api_params):
        import requests
        api_params["Timestamp"] = int(time.time())
        api_params["SecretId"] = self.secret_id
        api_params["Nonce"] = random.randint(10000, 99999)
        signature = self._append_signature(endpoint, api_params)
        params = copy.copy(api_params)
        params["Signature"] = signature
        resp = requests.get("https://" + endpoint, params=params, timeout=30)
        data = json.loads(resp.content)
        if "Error" in data["Response"]:
            error_msg = data["Response"]["Error"]["Message"]
            return {"result": False, "message": error_msg}
        else:
            return {"result": True, "data": data["Response"]}

    def get_regions(self):
        endpoint = "cvm.tencentcloudapi.com"
        api_params = {"Action": "DescribeRegions", "Version": "2017-03-12"}
        result = self.send_request(endpoint, api_params)
        return result

    def get_basic_alarms(self, start_time, end_time, **extra_params):
        endpoint = "monitor.tencentcloudapi.com"
        api_params = {
            "Action": "DescribeBasicAlarmList",
            "StartTime": start_time,
            "EndTime": end_time,
            "Limit": 1000,
            "Offset": 0,
            "Region": "ap-chengdu",
            "Module": "monitor",
            "Version": "2018-07-24",
        }
        api_params.update(**extra_params)
        result = self.send_request(endpoint, api_params)
        if result["result"]:
            total_num = result["data"]["Total"]
            alarm_list = result["data"]["Alarms"]
            if total_num > 1000:
                # 首页（Offset=0）已在上面取过，这里只补剩余页数。
                # py3 下 `/` 是浮点除法，直接喂给 range() 会抛 TypeError（GitLab #324），
                # 必须整数向上取整：总页数 ceil(total_num / 1000) 再减去已取的首页。
                request_count = math.ceil(total_num / 1000) - 1
                for _ in range(request_count):
                    api_params["Offset"] += 1000
                    result = self.send_request(endpoint, api_params)
                    if result["result"]:
                        temp_list = result["data"]["Alarms"]
                        alarm_list.extend(temp_list)
                    else:
                        return result
            return {"result": True, "alarm_list": alarm_list}
        else:
            return result
