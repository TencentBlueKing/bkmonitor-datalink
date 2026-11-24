# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""真实 HTTP 同步执行、鉴权和容量拒绝，不依赖外部服务。"""

import http.client
import json
import threading
import unittest
from unittest.mock import Mock

from linkd_runtime.executor import Executor
from linkd_runtime.http_server import RuntimeServer
from linkd_runtime.errors import TemporaryError


class HTTPTest(unittest.TestCase):
    def setUp(self):
        self.server = RuntimeServer(("127.0.0.1", 0), Executor(), "token", concurrency=1, max_body_bytes=1024)
        self.thread = threading.Thread(target=self.server.serve_forever, kwargs={"poll_interval": 0.01})
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.thread.join(timeout=2)
        self.server.server_close()

    def request(self, data, token="token"):
        connection = http.client.HTTPConnection(*self.server.server_address, timeout=2)
        try:
            connection.request("POST", "/execute", data, {"Authorization": "Bearer " + token})
            response = connection.getresponse()
            return response.status, response.read()
        finally:
            connection.close()

    def test_sync(self):
        request = {"version": 1, "request_id": "r", "bk_tenant_id": "tenant", "source_id": "source", "plugin": "rest_api_push", "mode": "push", "data": {"action": "heartbeat"}}
        status, body = self.request(json.dumps(request))
        self.assertEqual(status, 200)
        self.assertEqual(json.loads(body)["events"], [])

    def test_unauthorized(self):
        self.assertEqual(self.request("{}", "wrong")[0], 401)

    def test_large_request(self):
        self.assertEqual(self.request("x" * 1025)[0], 413)

    def test_invalid_json(self):
        self.assertEqual(self.request("invalid")[0], 400)

    def test_busy_does_not_queue(self):
        self.server.slots.acquire()
        try:
            self.assertEqual(self.request("{}")[0], 503)
        finally:
            self.server.slots.release()

    def test_temporary_failure_returns_without_retry(self):
        executor = Mock()
        executor.execute.side_effect = TemporaryError("private credential")
        self.server.executor = executor
        with self.assertLogs("linkd_runtime.http_server", level="ERROR") as logs:
            status, body = self.request('{"request_id":"r","bk_tenant_id":"t","source_id":"s"}')
        self.assertEqual(status, 200)
        self.assertEqual(json.loads(body)["error"]["code"], "temporary_error")
        executor.execute.assert_called_once()
        self.assertNotIn("private credential", str(logs.output))
        self.assertNotIn(b"private credential", body)
