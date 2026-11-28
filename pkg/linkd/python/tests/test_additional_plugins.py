# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""Prometheus 和 SolarWinds 的来源语义与失败释放。"""

from datetime import datetime, timezone
from types import SimpleNamespace
from unittest.mock import Mock, patch
import unittest

from linkd_runtime.executor import Executor


class AdditionalPluginsTest(unittest.TestCase):
    def request(self, plugin, mode, **fields):
        return {"version": 1, "request_id": "r", "bk_tenant_id": "tenant", "source_id": "source-id", "plugin": plugin, "mode": mode, **fields}

    def test_prometheus_trigger_and_recovery(self):
        alarm = {"fingerprint": "upstream-identity", "labels": {"ip": "127.0.0.1", "alertname": "cpu", "severity": "1"}, "annotations": {"summary": "CPU", "description": "high"}, "startsAt": "2026-11-14T12:00:00Z", "endsAt": "2026-11-14T12:05:00Z"}
        request = self.request("prometheus", "push", data={"alerts": [alarm, {**alarm, "status": "resolved"}]})
        events = Executor().execute(request)["events"]
        self.assertEqual([e["fingerprint"] for e in events], ["upstream-identity"] * 2)
        self.assertEqual([e["evaluations"][0]["action"] for e in events], ["triggered", "resolved"])
        self.assertEqual(events[1]["occurred_at"], "2026-11-14T12:05:00+00:00")
        self.assertEqual(events[0]["dimensions"], alarm["labels"])
        self.assertEqual(events[0]["evaluations"][0]["severity"], "fatal")

    def test_prometheus_missing_time_fails(self):
        with self.assertRaises(KeyError):
            Executor().execute(self.request("prometheus", "push", data={"alerts": [{"annotations": {}, "labels": {}}]}))

    def test_solarwinds_queries_and_closes(self):
        connection = Mock()
        cursor = connection.cursor.return_value
        active = [None] * 10
        active[5], active[6], active[7], active[9] = 42, datetime(2026, 11, 14, tzinfo=timezone.utc), "device 10.0.0.1 failed", 2
        cursor.fetchall.return_value = [active]
        cursor.fetchone.return_value = [None, None, None, None, "节点故障（详情）"]
        driver = SimpleNamespace(connect=Mock(return_value=connection))
        with patch.dict("sys.modules", pymssql=driver):
            event = Executor().execute(self.request("solarwinds", "pull", plugin_source_id="original-kac-source", configs={"server": "sql.invalid"}))["events"][0]
        self.assertEqual(event["fingerprint"], "original:42")
        self.assertEqual(event["dimensions"], {"ip": "10.0.0.1"})
        self.assertEqual(event["evaluations"][0]["severity"], "fatal")
        self.assertEqual(cursor.execute.call_args.args[1], (active[7],))
        connection.close.assert_called_once()

    def test_solarwinds_failure_closes_connection(self):
        connection = Mock()
        connection.cursor.return_value.execute.side_effect = RuntimeError("database failed")
        with patch.dict("sys.modules", pymssql=SimpleNamespace(connect=Mock(return_value=connection))), self.assertRaises(RuntimeError):
            Executor().execute(self.request("solarwinds", "pull"))
        connection.close.assert_called_once()
