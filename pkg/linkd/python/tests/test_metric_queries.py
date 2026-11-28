# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""指标宿主真实 HTTP 协议、错误及字节预算，不连接实际 KAC/KMC。"""

from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
from pathlib import Path
import tempfile
import threading
import unittest
from urllib.error import HTTPError

from linkd_runtime.executor import Executor
from linkd_runtime.metric_queries import MetricQueries


class MetricQueriesTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.token = Path(self.directory.name) / "token"
        self.token.write_text("signed-jwt")
        self.calls = []
        self.status = 200
        self.value = None
        owner = self
        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                query = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                owner.calls.append((query, self.headers.get("Internal-Token")))
                body = json.dumps({"data": owner.value}).encode()
                self.send_response(owner.status)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            def log_message(self, *_args):
                pass
        self.server = HTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, kwargs={"poll_interval": 0.01})
        self.thread.start()
        self.reader = MetricQueries("http://127.0.0.1:%d/plugin-queries/" % self.server.server_port, str(self.token))

    def tearDown(self):
        self.server.shutdown()
        self.thread.join(timeout=2)
        self.server.server_close()
        self.directory.cleanup()

    def test_operations_and_token_rotation(self):
        start, end = datetime(2026, 11, 14, tzinfo=timezone.utc), datetime(2026, 11, 15, tzinfo=timezone.utc)
        self.assertIsNone(self.reader.query_builtin("tenant", {"alarm_id": "a"}, start, end))
        self.value = {"restored": True}
        self.assertEqual(self.reader.recovery_fields("tenant", "a", "record"), self.value)
        self.token.write_text("Bearer rotated-jwt")
        self.value = {"series": []}
        self.assertEqual(self.reader.query_points("tenant", {"metric_name": "cpu"}), self.value)
        self.assertEqual([call[0]["operation"] for call in self.calls], ["query_builtin", "recovery_fields", "query_points"])
        self.assertTrue(all(call[0]["bk_tenant_id"] == "tenant" for call in self.calls))
        self.assertEqual(self.calls[0][0]["params"]["start_time"], start.isoformat())
        self.assertEqual(self.calls[-1][1], "Bearer rotated-jwt")

    def test_failed_history_does_not_fallback_to_points(self):
        self.status = 503
        with self.assertRaises(HTTPError):
            Executor(metrics=self.reader).execute({"version": 1, "request_id": "r", "bk_tenant_id": "tenant", "source_id": "built_in_bk", "plugin": "bs_monitor", "mode": "metric", "configs": {"username": "operator"}, "data": {"alarm": {"source_id": "built_in_bk"}, "start_time": "2026-11-14T12:00:00Z", "end_time": "2026-11-14T13:00:00Z"}})
        self.assertEqual(len(self.calls), 1)
        self.assertEqual(self.calls[0][0]["operation"], "query_builtin")

    def test_response_budget_fails_instead_of_partial_data(self):
        self.value = {"series": [1] * 100}
        reader = MetricQueries(self.reader.endpoint, str(self.token), max_bytes=10)
        with self.assertRaises(ValueError):
            reader.query_points("tenant", {})

    def test_worker_receives_metric_capability(self):
        from linkd_runtime.executor import ExecutorFactory
        from linkd_runtime.process_executor import ProcessExecutor
        self.value = [{"series": []}]
        executor = ProcessExecutor(timeout=2, factory=ExecutorFactory(metrics=self.reader))
        try:
            result = executor.execute({"version": 1, "request_id": "r", "bk_tenant_id": "tenant", "source_id": "built_in_bk", "plugin": "bs_monitor", "mode": "metric", "data": {"alarm": {"source_id": "built_in_bk"}, "start_time": "2026-11-14T12:00:00Z", "end_time": "2026-11-14T13:00:00Z"}})
            self.assertEqual(result["data"], self.value)
        finally:
            executor.close()

    def test_debug_metric_uses_optional_host(self):
        import subprocess
        import sys
        self.value = [{"series": []}]
        request = Path(self.directory.name) / "request.json"
        request.write_text(json.dumps({"version": 1, "request_id": "debug", "bk_tenant_id": "tenant", "source_id": "built_in_bk", "plugin": "bs_monitor", "mode": "metric", "data": {"alarm": {"source_id": "built_in_bk"}, "start_time": "2026-11-14T12:00:00Z", "end_time": "2026-11-14T13:00:00Z"}}))
        result = subprocess.run([sys.executable, "-S", "-m", "linkd_runtime", "debug", "--request", str(request), "--metric-url", self.reader.endpoint, "--metric-token-file", str(self.token)], capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["data"], self.value)
