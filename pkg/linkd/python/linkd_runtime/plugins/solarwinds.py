# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

import re

from linkd_runtime.base import BaseClientV2


class AlarmClient(BaseClientV2):
    """SQL Server 来源查询直接生成 standard。"""

    ALARM_SOURCE_NEED_CONFIGS = True

    def pull_raw_events(self):
        import pymssql

        # 1. 完整查询本轮来源记录，任何查询失败都不能提交部分批次。
        conn = pymssql.connect(**self.alarm_source_config_dict)
        try:
            cursor = conn.cursor()
            cursor.execute("select * from dbo.AlertActive")
            records = cursor.fetchall()
            events = []
            for active in records:
                cursor.execute("select * from dbo.AlertHistoryView where Message = %s", (active[7],))
                history = cursor.fetchone()
                events.append(self._standard_event({"active": active, "history": history}))
            return events
        finally:
            # 2. 连接始终归还；Kafka ACK 与统计由 Linkd 宿主处理。
            conn.close()

    def _standard_event(self, record):
        active, history = record["active"], record["history"]
        message, title = active[7], history[4]
        match = re.search(r"(?:25[0-5]\.|2[0-4]\d\.|[01]?\d\d?\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)", message)
        address = match[0] if match else ""
        return {
            "fingerprint": f"{self.alarm_source_id[:8]}:{active[5]}",
            "title": title,
            "content": message,
            "evaluations": [{"action": "triggered", "severity": {2: "fatal", 1: "warning"}.get(active[9], "remind")}],
            "occurred_at": self.source_time(active[6]),
            "dimensions": {"ip": address},
            "subject": {"name": address},
            "extra_data": {
                "item": title.split("（")[0],
                "record_id": str(active[5]),
                "message": message,
                "ip": address,
            },
        }
