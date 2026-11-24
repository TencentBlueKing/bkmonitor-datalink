# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

# -*- coding: utf-8 -*-
import time
from datetime import datetime, timedelta

import logging
from zoneinfo import ZoneInfo

from linkd_runtime.base import BaseClientV2
logger = logging.getLogger(__name__)


ALARM_LEVEL_MAPPING = {
    "level1": "remind",
    "level2": "warning",
    "level3": "fatal",
}

ALARM_ACTION_MAPPING = {
    0: "firing",
    1: "resolved",
}


class APIError(Exception):
    def __init__(self, message):
        self.message = message

    def __str__(self):
        return "APIError: {}".format(self.message)


class UCApi(object):
    def __init__(self, username, password, url):
        self.__username = username
        self.__password = password
        self.url = url.rstrip("/")
        self.__token = self.__get_token()

    def __get_token(self):
        import requests
        header = {"Content-Type": "application/json", "Accept": "application/json"}
        data = {"userName": self.__username, "passWord": self.__password}
        try:
            response = requests.post(
                "{}/token/generate".format(self.url),
                json=data,
                headers=header,
                verify=False,
                timeout=30,
            )
            response.raise_for_status()

            try:
                result = response.json()
            except ValueError as error:
                raise APIError("JSON解析失败: {}, 响应内容: {}".format(error, response.text))

            if result.get("token"):
                return result.get("token")
            raise APIError("获取Token失败: {}".format(result))
        except requests.RequestException as error:
            raise APIError("请求Token失败: {}".format(error))
        except APIError:
            raise
        except Exception as error:
            raise APIError("获取Token时发生未知错误: {}".format(error))

    def pull_alarms(self, time_from, time_till):
        import requests
        headers = {
            "Content-Type": "application/json",
            "Accept": "application/json",
            "X-Auth-Token": self.__token,
        }
        data = {"startAlarmTime": time_from, "endAlarmTime": time_till}
        try:
            response = requests.post(
                "{}/alarmrs/fullFault".format(self.url),
                json=data,
                headers=headers,
                timeout=60,
            )
            response.raise_for_status()

            try:
                result = response.json()
            except ValueError as error:
                raise APIError("JSON解析失败: {}, 响应内容: {}".format(error, response.text))

            return result.get("data", [])
        except requests.RequestException as error:
            raise APIError("拉取告警数据失败: {}".format(error))
        except APIError:
            raise
        except Exception as error:
            raise APIError("拉取告警时发生未知错误: {}".format(error))

    def check_alarms(self, alarm_ids):
        import requests
        headers = {
            "Content-Type": "application/json",
            "Accept": "application/json",
            "X-Auth-Token": self.__token,
        }
        data = {"ids": ",".join(alarm_ids)}
        try:
            response = requests.post(
                "{}/alarmrs/fullFault/batch/history".format(self.url),
                json=data,
                headers=headers,
                timeout=30,
            )
            response.raise_for_status()

            try:
                result = response.json()
            except ValueError as error:
                raise APIError("JSON解析失败: {}, 响应内容: {}".format(error, response.text))

            return result.get("data", [])
        except requests.RequestException as error:
            raise APIError("根据id获取告警失败: {}".format(error))
        except APIError:
            raise
        except Exception as error:
            raise APIError("检查告警状态时发生未知错误: {}".format(error))

    def logout(self):
        import requests
        headers = {
            "Content-Type": "application/json",
            "Accept": "application/json",
            "X-Auth-Token": self.__token,
        }
        data = {"token": self.__token}
        try:
            response = requests.post("{}/token/cancel".format(self.url), headers=headers, json=data, timeout=10)
            if response.status_code != 200:
                logger.warning("UC 注销登录失败")
        except Exception as error:
            logger.warning("UC 注销登录时发生错误: %s", type(error).__name__)


class AlarmClient(BaseClientV2):
    ALARM_SOURCE_NEED_CONFIGS = True

    def __init__(self, alarm_source_obj):
        super(AlarmClient, self).__init__(alarm_source_obj)

    def _fetch_source_records(self):
        username = self.alarm_source_config_dict.get("username", "")
        password = self.alarm_source_config_dict.get("password", "")
        url = self.alarm_source_config_dict.get("url", "")

        end_time_dt = datetime.fromisoformat(self.alarm_source_obj.received_at.replace("Z", "+00:00"))
        start_time_dt = end_time_dt - timedelta(days=1)
        start_time = int(start_time_dt.timestamp())
        end_time = int(end_time_dt.timestamp())
        self._pull_end_timestamp = end_time

        if self.alarm_source_obj.meta_info:
            start_time = int(self.alarm_source_obj.meta_info)

        formated_start_time = datetime.fromtimestamp(start_time, ZoneInfo(self.alarm_source_obj.timezone)).strftime("%Y-%m-%d %H:%M")
        formated_end_time = datetime.fromtimestamp(end_time, ZoneInfo(self.alarm_source_obj.timezone)).strftime("%Y-%m-%d %H:%M")

        self._source_records = []
        uc_client = UCApi(username, password, url)
        try:
            self._source_records = uc_client.pull_alarms(time_from=formated_start_time, time_till=formated_end_time)
            logger.debug("UC 拉取到 %d 条告警", len(self._source_records))
        except APIError as error:
            raise APIError("拉取告警数据失败: {}".format(error))
        finally:
            uc_client.logout()

    @staticmethod
    def _source_item(alarm):
        return alarm["faultConf"].get("faultName")

    @staticmethod
    def _source_name(alarm):
        return alarm["faultConf"].get("faultName")

    @staticmethod
    def _source_event_id(alarm):
        return str(alarm["serialNo"])

    @staticmethod
    def _source_alarm_time(alarm):
        return int(alarm["faultTimeReach"])

    @staticmethod
    def _source_content(alarm):
        return alarm.get("descInfo", "")

    @staticmethod
    def _source_device_name(alarm):
        return alarm.get("devName", "")

    @staticmethod
    def _source_action(alarm):
        return ALARM_ACTION_MAPPING.get(alarm["ackType"])

    @staticmethod
    def _source_level(alarm):
        level = "level1"
        severity = alarm.get("severity", 5)
        if severity == 5:
            level = "level1"
        elif 2 <= severity <= 4:
            level = "level2"
        elif severity == 1:
            level = "level3"
        return ALARM_LEVEL_MAPPING.get(level, "remind")

    @staticmethod
    def _source_object(alarm):
        return alarm["resSourceIp"] if alarm.get("resSourceIp") else alarm.get("devIp", "")

    @staticmethod
    def _source_bk_obj_id(alarm):
        return alarm.get("bk_obj_id", "")

    @staticmethod
    def _source_bk_inst_id(alarm):
        return alarm.get("bk_inst_id", "")

    @staticmethod
    def _source_meta_info(alarm):
        return alarm.get("meta_info", "")

    @staticmethod
    def _source_dimension_info(alarm):
        return alarm.get("dimension_info", "")

    def proposed_state(self):
        if hasattr(self, "_pull_end_timestamp"):
            return {"meta_info": self._pull_end_timestamp}
        return None

    def _fetch_recovery(self):
        alarm_source_obj = self.alarm_source_obj
        config = alarm_source_obj.get_config_dict()
        username = config.get("username", "")
        password = config.get("password", "")
        url = config.get("url", "")

        uc_client = UCApi(username, password, url)
        try:
            active_alarms = self.query_alarm_source_not_close_alarm(alarm_source_obj)
            if not active_alarms:
                return

            active_alarm_ids = [alarm["event_id"] for alarm in active_alarms]
            recovered_alarm_list = []
            recovered_alarm_id_list = []

            alarms = uc_client.check_alarms(active_alarm_ids)
            for alarm in alarms:
                if alarm["ackType"] == 1:
                    recovered_alarm_id_list.append(str(alarm["serialNo"]))

            for alarm in active_alarms:
                if alarm["event_id"] in recovered_alarm_id_list:
                    recovered_alarm_list.append(alarm)

            if recovered_alarm_list:
                self.match_alarm_resolved(recovered_alarm_list)
        except APIError as error:
            logger.error("UC 告警恢复失败: %s", type(error).__name__)
            raise
        except Exception as error:
            logger.error("UC 告警恢复时发生未知错误: %s", type(error).__name__)
            raise
        finally:
            uc_client.logout()

    @staticmethod
    def get_custom_msg_fields():
        return []

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
        action = self._source_action(alarm)
        action = {"firing": "triggered", "close": "closed"}.get(action, action)
        if action == "heartbeat":
            return {"action": "heartbeat"}
        extra = dict(alarm)
        extra["bk_inst_id"] = self._source_bk_inst_id(alarm)
        extra["bk_obj_id"] = self._source_bk_obj_id(alarm)
        extra["device_name"] = self._source_device_name(alarm)
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
