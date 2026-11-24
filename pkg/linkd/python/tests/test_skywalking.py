# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""隔离第三方 HTTP，验证分页完整性和发布前候选进度。"""

import sys
import unittest
from types import SimpleNamespace
from unittest.mock import Mock, patch

from linkd_runtime.executor import Executor
from linkd_runtime.errors import TemporaryError


class SkyWalkingTest(unittest.TestCase):
    def setUp(self):
        self.request = {"version": 1, "request_id": "pull-1", "bk_tenant_id": "tenant", "source_id": "sky", "plugin": "skywalking_graphql", "mode": "pull", "received_at": "2026-11-14T12:00:00Z", "configs": {"oap_url": "http://oap.invalid"}, "state": {"meta_info": 1700000000}, "timezone": "UTC"}
        self.http = SimpleNamespace(post=Mock(), Timeout=TimeoutError, ConnectionError=ConnectionError)

    def response(self, messages):
        response = Mock()
        response.json.return_value = {"data": {"getAlarm": {"msgs": messages}}}
        return response

    def test_all_pages_and_candidate_progress(self):
        messages = [{"id": str(i), "name": "service", "startTime": 1700000000000} for i in range(100)]
        self.http.post.side_effect = [self.response(messages), self.response([])]
        with patch.dict(sys.modules, requests=self.http):
            result = Executor().execute(self.request)
        self.assertEqual(len(result["events"]), 100)
        self.assertEqual(self.http.post.call_count, 2)
        self.assertEqual(result["proposed_state"]["meta_info"], 1794657600)
        self.assertEqual(self.request["state"]["meta_info"], 1700000000)
        windows = [call.kwargs["json"]["variables"]["duration"] for call in self.http.post.call_args_list]
        self.assertEqual(windows[0], windows[1])
        self.assertEqual(windows[0]["start"], "2023-11-14 2213")
        self.assertEqual(windows[0]["end"], "2026-11-14 1200")

    def test_empty_pull_can_advance(self):
        self.http.post.return_value = self.response([])
        with patch.dict(sys.modules, requests=self.http):
            result = Executor().execute(self.request)
        self.assertEqual(result["events"], [])
        self.assertIsInstance(result["proposed_state"]["meta_info"], int)

    def test_second_page_failure_returns_no_partial_batch(self):
        self.http.post.side_effect = [self.response([{}] * 100), TimeoutError("secret")]
        with patch.dict(sys.modules, requests=self.http), self.assertRaises(TemporaryError):
            Executor().execute(self.request)
        self.assertEqual(self.request["state"]["meta_info"], 1700000000)

    def test_graphql_error_is_not_success(self):
        response = self.response([])
        response.json.return_value = {"errors": [{"message": "failure"}]}
        self.http.post.return_value = response
        with patch.dict(sys.modules, requests=self.http), self.assertRaises(ValueError):
            Executor().execute(self.request)

    def test_missing_fixed_time_is_rejected_before_network(self):
        request = dict(self.request)
        request.pop("received_at")
        with patch.dict(sys.modules, requests=self.http), self.assertRaises(ValueError):
            Executor().execute(request)
        self.http.post.assert_not_called()

    def test_initial_window_uses_source_timezone(self):
        request = {**self.request, "state": {}, "timezone": "Asia/Shanghai"}
        self.http.post.return_value = self.response([])
        with patch.dict(sys.modules, requests=self.http):
            first = Executor().execute(request)
            second = Executor().execute(request)
        self.assertEqual(first["proposed_state"], second["proposed_state"])
        for call in self.http.post.call_args_list:
            self.assertEqual(call.kwargs["json"]["variables"]["duration"], {"start": "2026-11-14 1900", "end": "2026-11-14 2000", "step": "MINUTE"})
