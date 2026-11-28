# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

import hashlib
import json

from datetime import datetime, timedelta, timezone
from linkd_runtime.base import BaseClientV2


BK_MONITOR_LEVEL_DICT = {
    1: 'fatal',
    2: 'warning',
    3: 'remind',
}


class AlarmClient(BaseClientV2):
    """
    获取蓝鲸监控告警信息
    """

    def __init__(self, alarm_source_obj):
        super().__init__(alarm_source_obj)
        self.pull_alarm_url = self.alarm_source_config_dict["alarm_url"]
        # 旧 KAC 的 timezone.now() 在 USE_TZ 下为 UTC；API 时间参数没有偏移量，
        # 必须先统一到 UTC，避免同一时刻的不同 ISO 表示改变拉取窗口。
        self.now = datetime.fromisoformat(alarm_source_obj.received_at.replace("Z", "+00:00")).astimezone(timezone.utc)
        self.pulled = False

    def _fetch_source_records(self):
        # 开始同步蓝鲸监控告警
        page_response = self.send_get_request()
        if not page_response.get("result", False):
            raise RuntimeError(page_response.get("message", ""))

        self._source_records = page_response.get("data", [])
        if self._source_records is None:
            self._source_records = []
        self.pulled = True

    def proposed_state(self):
        if not self.pulled:
            return None
        last = self._source_records[-1] if self._source_records else {}
        return {"meta_info": last.get("id", self.alarm_source_obj.meta_info), "activate_time": self.now.isoformat()}

    def send_get_request(self):
        import requests
        # 蓝鲸监控告警事件入库延迟时间
        bk_monitor_delay_time = 5
        start_time = (datetime.fromisoformat(self.alarm_source_obj.activate_time).astimezone(timezone.utc) if self.alarm_source_obj.activate_time else self.now) - timedelta(
            minutes=(self.alarm_source_obj.celery_task_time_interval + bk_monitor_delay_time)
        )

        request_params = dict(
            **{
                "bk_app_code": self.alarm_source_config_dict["app_code"],
                "bk_app_secret": self.alarm_source_config_dict["app_secret"],
                "bk_username": self.alarm_source_config_dict["username"],
                "page_size": -1,
                "user_status__in": "notified,unnotified",
                "status": "converged",
            }
        )

        # 如果上次同步时间距离现在太久, 为避免接口超时, 按照时间参数拉取
        if start_time < self.now - timedelta(hours=12):
            start_time = self.now - timedelta(hours=12)
            request_params["source_time__gt"] = start_time.strftime("%Y-%m-%d %H:%M:%S")
            request_params["source_time__lte"] = self.now.strftime("%Y-%m-%d %H:%M:%S")
        # 否则判断是否存储有上次同步事件ID, 有则按事件ID参数拉取, 否则按照时间参数拉取
        else:
            if self.alarm_source_obj.meta_info:
                request_params["id__gt"] = self.alarm_source_obj.meta_info
            else:
                request_params["source_time__gt"] = start_time.strftime("%Y-%m-%d %H:%M:%S")
                request_params["source_time__lte"] = self.now.strftime("%Y-%m-%d %H:%M:%S")

        resp = requests.get(self.pull_alarm_url, params=request_params, verify=False, timeout=30)
        if resp.status_code == 200:
            res_data = json.loads(resp.content)
            return res_data
        else:
            raise RuntimeError("pull alarm request error, status_code: %s" % resp.status_code)

    @staticmethod
    def _source_item(alarm):
        return alarm.get("alarm_content", {}).get("source_name", "")

    @staticmethod
    def _source_name(alarm):
        return alarm.get("alarm_content", {}).get("title", "")

    @staticmethod
    def _source_event_id(alarm):
        return hashlib.md5(
            "{}{}".format(alarm.get("level", 3), json.dumps(alarm["origin_alarm"]["dimensions"])).encode("utf8")
        ).hexdigest()

    @staticmethod
    def _source_alarm_time(alarm):
        return alarm.get("source_time", "")

    @staticmethod
    def _source_content(alarm):
        alarm_content = alarm.get("alarm_content", {})
        return "{}-{}".format(alarm_content.get("dimension", ""), alarm_content.get("alarm_message", ""))

    @staticmethod
    def _source_level(alarm):
        return BK_MONITOR_LEVEL_DICT.get(alarm.get("level", 3))

    @staticmethod
    def _source_object(alarm):
        return alarm.get("ip", "")

    @staticmethod
    def _source_bk_biz_id(alarm):  # noqa
        return alarm.get("bk_biz_id", 0)

    # 需要修改
    @staticmethod
    def _source_bk_set_id(alarm):  # noqa
        return alarm.get("cc_topo_set", "")

    # 需要修改
    @staticmethod
    def _source_bk_module_id(alarm):  # noqa
        return alarm.get("cc_app_module", "")

    @staticmethod
    def _get_alarm_dimension(alarm):  # noqa
        return alarm.get("alarm_dimension", {})

    def _source_bk_biz_name(self, alarm):
        bk_biz_name = ""
        bk_biz_id = alarm.get("bk_biz_id")
        if not bk_biz_id:
            return bk_biz_name

        biz_name_list = self._get_alarm_dimension(alarm).get("业务", [])
        if biz_name_list:
            return "{}[{}]".format(biz_name_list[0], bk_biz_id)
        return bk_biz_name

    def _source_bk_set_name(self, alarm):
        bk_set_name = ""
        bk_set_id = alarm.get("cc_topo_set")
        if not bk_set_id:
            return bk_set_name

        set_name_list = self._get_alarm_dimension(alarm).get("集群", [])
        if set_name_list:
            return "{}[{}]".format(set_name_list[0], bk_set_id)
        return bk_set_name

    def _source_bk_module_name(self, alarm):
        """
        TODO 蓝鲸监控接口未返回模块名称信息，暂不做清洗
        :param alarm:
        :return:
        """
        return ""

    @staticmethod
    def _source_bk_cloud_id(alarm):  # noqa
        return alarm.get("bk_cloud_id", 0)

    @staticmethod
    def _source_meta_info(alarm):
        """将蓝鲸监控事件id存盘, 用来告警自恢复, 查询字段"""
        return alarm.get("id", "")

    def _fetch_recovery(self):
        import requests

        def query_recovery(alarms):
            candidates = {str(alarm["meta_info"]): alarm for alarm in alarms if alarm["meta_info"]}
            if not candidates:
                return []
            response = requests.get(
                self.pull_alarm_url,
                params={
                    "bk_app_code": self.alarm_source_config_dict["app_code"],
                    "bk_app_secret": self.alarm_source_config_dict["app_secret"],
                    "bk_username": self.alarm_source_config_dict["username"],
                    "page_size": -1,
                    "status": "recovered",
                    "id__in": ",".join(candidates),
                },
                verify=False,
                timeout=30,
            )
            if response.status_code != 200:
                raise RuntimeError(f"recovery query status: {response.status_code}")
            body = response.json()
            if not body.get("result"):
                raise RuntimeError(body.get("message", "recovery query failed"))
            return [candidates[str(alarm["id"])] for alarm in body.get("data", [])]

        # 1. 读取同租户活动候选，50 条一批保留 GET 请求长度边界。
        active = self.query_alarm_source_not_close_alarm(self.alarm_source_obj)
        batches = [active[index : index + 50] for index in range(0, len(active), 50)]
        # 2. 顺序查询并传播任一批错误，由执行器统一控制并发，不把失败伪装为无恢复。
        recovered = [query_recovery(batch) for batch in batches]
        # 3. 所有查询成功后才构造终态，统一交给基类校验和投递。
        for batch in recovered:
            self.match_alarm_resolved(batch)

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
        extra["bk_biz_id"] = self._source_bk_biz_id(alarm)
        extra["bk_biz_name"] = self._source_bk_biz_name(alarm)
        extra["bk_cloud_id"] = self._source_bk_cloud_id(alarm)
        extra["bk_module_id"] = self._source_bk_module_id(alarm)
        extra["bk_module_name"] = self._source_bk_module_name(alarm)
        extra["bk_set_id"] = self._source_bk_set_id(alarm)
        extra["bk_set_name"] = self._source_bk_set_name(alarm)
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
            "dimensions": dict(alarm["origin_alarm"]["dimensions"]),
            "labels": dict(alarm.get("labels") or {}),
            "extra_data": extra,
        }
        if extra.get("object"):
            event["subject"] = {"name": str(extra["object"])}
        return event
