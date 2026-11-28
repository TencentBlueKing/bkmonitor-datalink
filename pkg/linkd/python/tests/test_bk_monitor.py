# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""旧蓝鲸监控来源的配置注入、游标和恢复分批。"""

import json
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

from linkd_runtime.executor import Executor


class BKMonitorTest(unittest.TestCase):
    def request(self, **fields):
        return {"version": 1, "request_id": "r", "bk_tenant_id": "tenant", "source_id": "source", "plugin": "bk_monitor", "mode": "pull", "received_at": "2026-11-14T12:00:00Z", "interval_minutes": 5, "configs": {"alarm_url": "http://monitor.invalid/alarms", "app_code": "test", "app_secret": "private", "username": "operator"}, "state": {"meta_info": 42, "activate_time": "2026-11-14T11:55:00+00:00"}, **fields}

    def test_incremental_empty_pull_preserves_id(self):
        http = SimpleNamespace(get=Mock(return_value=Mock(status_code=200, content=json.dumps({"result": True, "data": []}).encode())))
        with patch.dict("sys.modules", requests=http):
            result = Executor().execute(self.request())
        self.assertEqual(result["events"], [])
        self.assertEqual(result["proposed_state"], {"meta_info": 42, "activate_time": "2026-11-14T12:00:00+00:00"})
        params = http.get.call_args.kwargs["params"]
        self.assertEqual(params["id__gt"], 42)
        self.assertEqual(params["bk_username"], "operator")

    def test_failed_pull_has_no_candidate_progress(self):
        http = SimpleNamespace(get=Mock(return_value=Mock(status_code=503)))
        with patch.dict("sys.modules", requests=http), self.assertRaises(RuntimeError):
            Executor().execute(self.request())

    def test_time_window_uses_utc_for_equivalent_input_offsets(self):
        # 旧 KAC 在 USE_TZ 下以 timezone.now() 的 UTC 时间构造无时区 API 参数。
        for received_at in ("2026-11-14T12:00:00Z", "2026-11-14T20:00:00+08:00"):
            for state, expected_start in (
                ({}, "2026-11-14 11:50:00"),
                ({"activate_time": "2026-11-14T19:55:00+08:00"}, "2026-11-14 11:45:00"),
                ({"meta_info": 42, "activate_time": "2026-11-13T20:00:00+08:00"}, "2026-11-14 00:00:00"),
            ):
                with self.subTest(received_at=received_at, state=state):
                    http = SimpleNamespace(get=Mock(return_value=Mock(status_code=200, content=b'{"result":true,"data":[]}')))
                    with patch.dict("sys.modules", requests=http):
                        result = Executor().execute(self.request(received_at=received_at, state=state))
                    params = http.get.call_args.kwargs["params"]
                    self.assertEqual(params["source_time__gt"], expected_start)
                    self.assertEqual(params["source_time__lte"], "2026-11-14 12:00:00")
                    self.assertNotIn("id__gt", params)
                    self.assertEqual(result["proposed_state"]["activate_time"], "2026-11-14T12:00:00+00:00")

    def test_recovery_batches_and_identity(self):
        alarms = [{"meta_info": i, "bk_tenant_id": "tenant", "source_id": "source", "fingerprint": str(i), "level": "warning"} for i in range(1, 52)]
        first, second = Mock(status_code=200), Mock(status_code=200)
        first.json.return_value = {"result": True, "data": [{"id": 1}]}
        second.json.return_value = {"result": True, "data": [{"id": 51}]}
        http = SimpleNamespace(get=Mock(side_effect=[first, second]))
        with patch.dict("sys.modules", requests=http):
            result = Executor(active_alerts=Mock(return_value=alarms)).execute(self.request(mode="recovery"))
        self.assertEqual([event["fingerprint"] for event in result["events"]], ["1", "51"])
        self.assertNotIn("proposed_state", result)
        self.assertEqual(len(http.get.call_args_list[0].kwargs["params"]["id__in"].split(",")), 50)

    def test_later_recovery_failure_is_not_partial_success(self):
        alarms = [{"meta_info": i, "bk_tenant_id": "tenant", "source_id": "source", "fingerprint": str(i), "level": "warning"} for i in range(1, 52)]
        first = Mock(status_code=200)
        first.json.return_value = {"result": True, "data": [{"id": 1}]}
        with patch.dict("sys.modules", requests=SimpleNamespace(get=Mock(side_effect=[first, TimeoutError()]))), self.assertRaises(TimeoutError):
            Executor(active_alerts=Mock(return_value=alarms)).execute(self.request(mode="recovery"))
