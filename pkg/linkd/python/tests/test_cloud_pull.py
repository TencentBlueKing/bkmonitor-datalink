# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""腾讯云/听云采集与恢复，不调用实际厂商 API。"""

from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

from linkd_runtime.executor import Executor


class CloudPullTest(unittest.TestCase):
    def request(self, plugin, **fields):
        return {"version": 1, "request_id": "r", "bk_tenant_id": "tenant", "source_id": "source", "plugin": plugin, "mode": "pull", "received_at": "2026-11-14T12:00:00Z", "timezone": "UTC", "interval_minutes": 5, "configs": {"secret_id": "test", "secret_key": "test"}, **fields}

    def test_qcloud_empty_pull_records_activation_after_success(self):
        api = Mock()
        api.get_basic_alarms.return_value = {"result": True, "alarm_list": []}
        with patch("linkd_runtime.plugins.qcloud_monitor.QCloudApi", return_value=api):
            result = Executor().execute(self.request("qcloud_monitor"))
        self.assertEqual(result["events"], [])
        self.assertEqual(result["proposed_state"]["activate_time"], "2026-11-14T12:00:00+00:00")
        start, end = api.get_basic_alarms.call_args.args
        self.assertEqual(end - start, 8 * 60)

    def test_qcloud_recovery_and_window_order(self):
        api = Mock()
        api.get_basic_alarms.return_value = {"result": True, "alarm_list": [{"Id": "cloud-1"}]}
        first = {"bk_tenant_id": "tenant", "source_id": "source", "fingerprint": "f1", "event_id": "f1", "meta_info": "cloud-1", "level": "warning", "alarm_time": "2026-11-14 11:00:00"}
        second = {**first, "meta_info": "cloud-2", "alarm_time": "2026-11-14 10:00:00"}
        with patch("linkd_runtime.plugins.qcloud_monitor.QCloudApi", return_value=api):
            result = Executor(active_alerts=Mock(return_value=[first, second])).execute(self.request("qcloud_monitor", mode="recovery"))
        self.assertEqual(len(result["events"]), 1)
        self.assertEqual(result["events"][0]["fingerprint"], "f1")
        self.assertNotIn("proposed_state", result)
        start, end = api.get_basic_alarms.call_args.args
        self.assertLess(start, end)

    def test_tingyun_empty_and_failure(self):
        for status in (200, 503):
            response = Mock(status_code=status)
            response.json.return_value = {"appList": []}
            with patch.dict("sys.modules", requests=SimpleNamespace(get=Mock(return_value=response))):
                if status == 200:
                    self.assertEqual(Executor().execute(self.request("tingyun"))["events"], [])
                else:
                    with self.assertRaises(RuntimeError):
                        Executor().execute(self.request("tingyun"))

    def test_tingyun_collects_all_apps_and_types(self):
        alarm = {"id": 42, "status": 1, "instanceName": "host 10.0.0.1", "metricId": 21, "targetName": "app", "eventLevel": 1, "beginTimeMills": 1794657600000, "value": 0.5}
        apps = Mock(status_code=200)
        apps.json.return_value = {"appList": [{"id": 1}, {"id": 2}]}
        page = Mock(status_code=200)
        page.json.return_value = {"events": [alarm, {**alarm, "status": 0}]}
        get = Mock(side_effect=[apps] + [page] * 6)
        with patch.dict("sys.modules", requests=SimpleNamespace(get=get)):
            events = Executor().execute(self.request("tingyun"))["events"]
        self.assertEqual(len(events), 6)
        self.assertTrue(all(event["evaluations"] == [{"action": "triggered", "severity": "warning"}] for event in events))
        self.assertTrue(all(event["subject"]["name"] == "10.0.0.1" for event in events))
        self.assertEqual(get.call_count, 7)
        for call in get.call_args_list:
            self.assertEqual(call.kwargs["timeout"], 30)

    def test_tingyun_partial_failure_does_not_return_partial_events(self):
        for failure in ("http", "json", "network"):
            with self.subTest(failure=failure):
                apps = Mock(status_code=200)
                apps.json.return_value = {"appList": [{"id": 1}]}
                first = Mock(status_code=200)
                # 故意省略标准化字段；若错误被吞掉，随后转换会报 KeyError。
                first.json.return_value = {"events": [{"status": 1}]}
                failed = Mock(status_code=503 if failure == "http" else 200)
                failed.json.side_effect = ValueError("invalid JSON")
                get = Mock(side_effect=[apps, first, OSError("connection lost") if failure == "network" else failed])
                expected = {"http": RuntimeError, "json": ValueError, "network": OSError}[failure]
                with patch.dict("sys.modules", requests=SimpleNamespace(get=get)), self.assertRaises(expected):
                    Executor().execute(self.request("tingyun"))
                self.assertEqual(get.call_count, 3)
