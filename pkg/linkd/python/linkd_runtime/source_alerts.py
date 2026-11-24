# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""读取 Linkd 来源活动告警，为恢复插件提供原有来源字段语义。"""

import copy
from dataclasses import dataclass
from datetime import datetime
import json
from pathlib import Path
from urllib.parse import urlencode
from urllib.request import Request
from .query_transport import open_query as _open_query
from zoneinfo import ZoneInfo


@dataclass(frozen=True)
class ActiveAlerts:
    """部署配置注入查询地址和内部凭据，不接受插件请求指定查询服务器。"""

    endpoint: str
    token: str = ""
    timeout: float = 10
    max_bytes: int = 32 << 20
    max_items: int = 10000
    max_pages: int = 1000
    timezone: str = "Asia/Shanghai"
    token_file: str | None = None

    def __call__(self, tenant, source, query):
        return self._query(tenant, source, query, active_only=True)

    def latest(self, tenant, source):
        """读取本来源最新告警，空历史返回 None；不把读取失败当作首次执行。"""
        alarms = self._query(tenant, source, {}, active_only=False)
        return max(alarms, key=lambda alarm: alarm["alarm_time"]) if alarms else None

    def _query(self, tenant, source, query, *, active_only):
        params = {"bk_tenant_id": tenant, "event_source_id": source, "limit": 32}
        if active_only:
            params["status"] = "active"
        remaining = self.max_bytes
        count = 0
        result = []
        for _ in range(self.max_pages):
            # 读取已签发的 Internal-Token，文件轮换不要求重启常驻执行进程。
            token = Path(self.token_file).read_text().strip() if self.token_file else self.token.strip()
            if not token:
                raise ValueError("source query token is required")
            header = token if token.startswith("Bearer ") else "Bearer " + token
            request = Request(self.endpoint.rstrip("/") + "/api/v1/alerts?" + urlencode(params), headers={"Internal-Token": header})
            with _open_query(request, timeout=self.timeout) as response:
                raw = response.read(remaining + 1)
            remaining -= len(raw)
            if remaining < 0:
                raise ValueError("source alert response exceeds byte budget")
            page = json.loads(raw)
            count += len(page["items"])
            if count > self.max_items:
                raise ValueError("source alert response exceeds item budget")
            for alert in page["items"]:
                # 这是外部查询边界，不能将其他租户/来源的数据用于恢复。
                if alert.get("bk_tenant_id") != tenant or alert.get("event_source_id") != source or (active_only and alert.get("status") != "active"):
                    raise ValueError("source alert scope mismatch")
                alarm = self._convert(alert, tenant, source)
                if all(alarm.get(key) in (value if isinstance(value, list) else [value]) for key, value in query.items()):
                    result.append(alarm)
            cursor = page["next"]
            if not cursor:
                return result
            params["after"] = cursor
        raise ValueError("source alert response exceeds page budget")

    def _convert(self, alert, tenant, source):
        extra = copy.deepcopy(alert.get("extra_data") or {})
        alarm = dict(extra)
        action = {"active": "firing", "recovered": "resolved", "closed": "close"}[alert["status"]]
        alarm.update(bk_tenant_id=tenant, source_id=source, event_id=alert["fingerprint"], fingerprint=alert["fingerprint"], extra_data=extra, alert_id=alert.get("source_alert_id") or alert["fingerprint"], linkd_alert_id=alert["alert_id"], action=action, source_alarm_status=action)
        for key in ("dimensions", "labels"):
            if key in alert:
                alarm[key] = alert[key]
        if alert.get("subject_id"):
            alarm["subject"] = {"system": alert.get("subject_system", ""), "type": alert.get("subject_type", ""), "id": alert["subject_id"], "name": alert.get("subject_name", "")}
        alarm.setdefault("name", alert.get("title", ""))
        alarm.setdefault("content", alert.get("content", ""))
        alarm.setdefault("level", alert.get("severity", ""))
        alarm.setdefault("object", alert.get("subject_name") or alert.get("subject_id", ""))
        alarm["alarm_time"] = datetime.fromisoformat(alert["begin_at"].replace("Z", "+00:00")).astimezone(ZoneInfo(self.timezone)).strftime("%Y-%m-%d %H:%M:%S")
        return alarm
