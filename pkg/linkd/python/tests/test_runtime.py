# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""独立解释器下验证转换、租户归属和失败边界。"""

import unittest
import os
import subprocess
import sys
import json

from linkd_runtime.base import BaseClientV2, Source, source_time
from linkd_runtime.plugins.tcop import AlarmClient as TCOPClient
from linkd_runtime.plugins.rest_api_push import AlarmClient


class RuntimeTest(unittest.TestCase):
    def setUp(self):
        self.source = Source("source", "测试来源", "tenant-a")

    def test_tenant_and_resource_conversion(self):
        event = {"fingerprint": "a", "title": "告警", "bk_tenant_id": "untrusted", "bk_inst_id": 42, "evaluations": [{"action": "triggered", "severity": "warning"}]}
        result = AlarmClient(self.source, event).execute()
        self.assertEqual(result[0]["bk_tenant_id"], "tenant-a")
        self.assertEqual(result[0]["extra_data"]["bk_inst_id"], "42")
        self.assertEqual(event["bk_tenant_id"], "untrusted")

    def test_heartbeat(self):
        self.assertEqual(AlarmClient(self.source, {"action": "heartbeat"}).execute(), [])

    def test_invalid_event_fails(self):
        with self.assertRaises(ValueError):
            AlarmClient(self.source, {"title": "missing evaluations"}).execute()

    def test_time_units(self):
        self.assertEqual(source_time(1700000000), source_time(1700000000000))
        self.assertTrue(source_time("2026-11-14T12:00:00").endswith("+08:00"))

    def test_source_timezones_are_independent(self):
        utc = BaseClientV2(Source("b", "UTC", "tenant-b", timezone="UTC"))
        local = BaseClientV2(self.source)
        self.assertEqual(utc.source_time("2026-11-14T12:00:00"), "2026-11-14T12:00:00+00:00")
        self.assertEqual(local.source_time("2026-11-14T12:00:00"), "2026-11-14T12:00:00+08:00")

    def test_tcop_stable_source_identity(self):
        data = {"sessionId": "session-1", "firstOccurTime": "2026-11-14T12:00:00"}
        event = TCOPClient(self.source, data).execute()[0]
        self.assertEqual(event["fingerprint"], "session-1")
        self.assertEqual(event["bk_tenant_id"], "tenant-a")

    def test_tcop_numeric_fingerprint_uses_source_timezone(self):
        # 独立解释器切换宿主 TZ，避免在多线程运行时内修改进程全局时区。
        script = """
import json
from linkd_runtime.base import Source
from linkd_runtime.plugins.tcop import AlarmClient
source = Source("source", "source", "tenant", timezone="Asia/Shanghai")
client = AlarmClient(source, {})
print(json.dumps([client._source_event_id({"firstOccurTime": value}) for value in
                 [1700000000, 1700000000000, "2023-11-15 06:13:20"]]))
"""
        fingerprints = []
        for zone in ("UTC", "Pacific/Honolulu"):
            output = subprocess.run([sys.executable, "-c", script], env={**os.environ, "TZ": zone},
                                    check=True, capture_output=True, text=True, timeout=10)
            fingerprints.extend(json.loads(output.stdout))
        self.assertEqual(len(set(fingerprints)), 1)

    def test_tcop_missing_identity_time_does_not_use_current_time(self):
        client = TCOPClient(self.source, {})
        for value in (None, "", 0, "0", " "):
            with self.subTest(value=value), self.assertRaises(ValueError):
                client._source_event_id({"firstOccurTime": value})

    def test_recovery_entry_rejects_triggered(self):
        class InvalidRecovery(BaseClientV2):
            def pull_recovery_events(self):
                return [{"fingerprint": "x", "title": "x", "evaluations": [{"action": "triggered", "severity": "warning"}]}]
        with self.assertRaises(ValueError):
            InvalidRecovery(self.source).execute(recovery=True)

    def test_pull_includes_recovery_and_does_not_accumulate(self):
        class Pull(BaseClientV2):
            def pull_raw_events(self):
                self._terminal_events.append({"fingerprint": "x", "title": "x", "evaluations": [{"action": "resolved", "severity": "warning"}]})
                return []
        client = Pull(self.source)
        self.assertEqual(len(client.execute()), 1)
        self.assertEqual(len(client.execute()), 1)


if __name__ == "__main__":
    unittest.main()
