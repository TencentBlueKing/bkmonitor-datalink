# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

import datetime
import hashlib

from linkd_runtime.base import BaseClientV2


LEVEL_DICT = {
    "mid": 'warning',
    "low": 'remind',
}


class AlarmClient(BaseClientV2):
    """日志易告警"""

    # 定义告警源获取告警是否需要额外配置信息
    ALARM_SOURCE_NEED_CONFIGS = True
    # 定义是否通过ip拿到主机在cmdb中的信息

    def __init__(self, alarm_source_obj):
        super().__init__(alarm_source_obj)
        self.alarm_source_name = alarm_source_obj.name

    def _fetch_source_records(self):
        self._source_records = self.pull_log_simple_alarm()

    def pull_log_simple_alarm(self):
        # 告警恢复机制: 拉取前6min的告警并且进行告警恢复
        alert_names = self.get_log_simple_recovery()
        if alert_names:
            resolved_alarms = self.query_alarm_source_not_close_alarm(
                self.alarm_source_obj, query={"alarm_type": alert_names}
            )
            self.match_alarm_resolved(resolved_alarms)

        # 普通日志告警
        return_alarm_list = self.get_log_simple_normal_alarm()
        return return_alarm_list

    def get_log_simple_normal_alarm(self):
        import requests
        """获取日志易常规告警"""
        url = "{}/v1/29846d2bde113073476b71ae7f838247/api/spl/search?task_name=test&category=search&time_range=-10m,now&query=index%3Dmonitor%20*%20NOT%20issue_alert%3Afalse%20alert_level%3A*%20(NOT%20alert_name%3A*%E6%95%85%E9%9A%9C%E6%81%A2%E5%A4%8D*%20)%20AND%20result.result.extend_hits.info%3A*%7Ceval%20time%3Dformatdate(timestamp)%7Cstats%20count()%20%20by%20time%2Calert_name%2Calert_id%2Calert_level%2Cresult.strategy.description%2Cresult.strategy.trigger.compare%2Cresult.strategy.trigger.alert_thresholds%2Cresult.result.extend_hits.info%0A%7Cappend%5B%5Bindex%3Dmonitor%20*%20NOT%20issue_alert%3Afalse%20alert_level%3A*%20(NOT%20alert_name%3A*%E6%95%85%E9%9A%9C%E6%81%A2%E5%A4%8D*%20)%20NOT%20result.result.extend_hits.info%3A*%7Ceval%20time%3Dformatdate(timestamp)%7Cstats%20count()%20%20by%20time%2Calert_name%2Calert_id%2Calert_level%2Cresult.strategy.description%2Cresult.strategy.trigger.compare%2Cresult.strategy.trigger.alert_thresholds%5D%5D%0A%7Cfields%20time%2Calert_name%2Calert_id%2Calert_level%2Cresult.strategy.description%2Cresult.strategy.trigger.compare%2Cresult.strategy.trigger.alert_thresholds%2Cresult.result.extend_hits.info&source_group=all&size=10000&fields=false".format(  # noqa
            self.alarm_source_config_dict.get("host", "")
        )

        auth = (self.alarm_source_config_dict.get("account", ""), self.alarm_source_config_dict.get("password", ""))
        res = requests.get(url=url, auth=auth, timeout=30)
        return_alarm_list = []

        # 告警去重机制: 通过告警时间对比过滤已经采集过的告警
        last_alarm = self.query_latest_source_alarm()
        last_alarm_time = None
        if last_alarm is not None:
            previous = datetime.datetime.strptime(last_alarm["alarm_time"], "%Y-%m-%d %H:%M:%S")
            last_alarm_time = (previous - datetime.timedelta(minutes=1)).strftime("%Y-%m-%d %H:%M:%S")

        if res.status_code == 200:
            res_data = res.json()
            rows = res_data.get("result").get("sheets").get("rows")
            if rows:
                for row in rows:
                    single_alarm = {key: row[key] for key in row if key != "result.result.extend_hits.info"}
                    if row.get("result.result.extend_hits.info"):
                        content_list = row["result.result.extend_hits.info"].split("||")
                        for ip_content in content_list:
                            alarm_copy = single_alarm.copy()
                            ip_content_list = ip_content.split("_")
                            if len(ip_content_list) >= 2:
                                alarm_copy["ip"] = ip_content_list[0]
                                alarm_copy["content"] = ip_content_list[1]
                            else:
                                alarm_copy["ip"] = ip_content
                                alarm_copy["content"] = alarm_copy["alert_name"]

                            alarm_time = datetime.datetime.strptime(alarm_copy["time"], "%Y-%m-%d:%H:%M:%S.%f")
                            alarm_time = alarm_time.strftime("%Y-%m-%d %H:%M:%S")
                            if last_alarm_time is None or alarm_time > last_alarm_time:
                                alarm_copy["alarm_time"] = alarm_time
                                return_alarm_list.append(alarm_copy)
                    else:
                        alarm_copy = single_alarm.copy()
                        alarm_copy.update({"ip": "", "content": alarm_copy["alert_name"]})

                        alarm_time = datetime.datetime.strptime(alarm_copy["time"], "%Y-%m-%d:%H:%M:%S.%f")
                        alarm_time = alarm_time.strftime("%Y-%m-%d %H:%M:%S")
                        if last_alarm_time is None or alarm_time > last_alarm_time:
                            alarm_copy["alarm_time"] = alarm_time
                            return_alarm_list.append(alarm_copy)
        else:
            raise RuntimeError("pull alarm request error, status_code: %s" % res.status_code)

        return return_alarm_list

    def get_log_simple_recovery(self):
        import requests
        """获取日志易的告警恢复"""
        try:
            url = "{}/v1/29846d2bde113073476b71ae7f838247/api/spl/search?task_name=test&category=search&time_range=-6m,now&query=index%3Dmonitor%20alert_name%3A*%7Ceval%20time%3Dformatdate(timestamp)%7Ctable%20time%2Calert_name%2Calert_id%20%2Cresult.is_alert_recovery%7Cwhere%20!empty(result.is_alert_recovery)%20%26%26%20result.is_alert_recovery%3D%3D%22true%22&source_group=all&size=10000&fields=false".format(  # noqa
                self.alarm_source_config_dict.get("host", "")
            )

            auth = (self.alarm_source_config_dict.get("account", ""), self.alarm_source_config_dict.get("password", ""))
            res = requests.get(url=url, auth=auth, timeout=30)
            if res.status_code == 200:
                res_data = res.json()
                rows = res_data.get("result").get("sheets").get("rows")
                alert_names = list({row["alert_name"] for row in rows})
                return alert_names
            else:
                raise RuntimeError("日志易恢复查询失败")
        except Exception:
            # 查询失败不能被解释成没有恢复记录。
            raise

    def _source_item(self, alarm):
        return alarm.get("alert_name", "")

    @staticmethod
    def _source_name(alarm):
        return alarm.get("result.strategy.description", "") + "-" + alarm.get("alert_name")

    def _source_event_id(self, alarm):
        return hashlib.md5(
            "{}:{}:{}".format(self.alarm_source_id[:12], alarm.get("alert_id"), alarm.get("ip")).encode("utf8")
        ).hexdigest()

    @staticmethod
    def _source_alarm_time(alarm):
        return alarm["alarm_time"]

    @staticmethod
    def _source_content(alarm):
        return alarm.get("content", "")

    @staticmethod
    def _source_level(alarm):
        return LEVEL_DICT.get(alarm["alert_level"], 'fatal')

    @staticmethod
    def _source_object(alarm):
        return alarm.get("ip", "")

    @staticmethod
    def _source_meta_info(alarm):
        return alarm.get("result.strategy.trigger.alert_thresholds", "")

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
