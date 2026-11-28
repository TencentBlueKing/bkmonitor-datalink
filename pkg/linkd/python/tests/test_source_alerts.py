# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""来源查询分页、租户边界和插件历史字段映射。"""

import io
import json
import unittest
from unittest.mock import patch
from urllib.parse import parse_qs, urlparse

from linkd_runtime.source_alerts import ActiveAlerts


class SourceAlertsTest(unittest.TestCase):
    def alert(self, **changes):
        return {"bk_tenant_id": "tenant", "event_source_id": "source", "status": "active", "fingerprint": "7", "alert_id": "internal", "severity": "warning", "begin_at": "2026-11-14T12:00:00Z", "extra_data": {"meta_info": "remote-7"}, **changes}

    def page(self, items, cursor=""):
        return io.BytesIO(json.dumps({"items": items, "next": cursor}).encode())

    def test_filter_continues_through_empty_matching_page(self):
        with patch("linkd_runtime.source_alerts._open_query", side_effect=[self.page([self.alert(fingerprint="other")], "cursor"), self.page([self.alert()])]) as fetch:
            result = ActiveAlerts("http://linkd.invalid", "token")("tenant", "source", {"event_id": ["7"]})
        self.assertEqual(result[0]["event_id"], "7")
        self.assertEqual(result[0]["meta_info"], "remote-7")
        self.assertEqual(result[0]["level"], "warning")
        self.assertEqual(result[0]["alarm_time"], "2026-11-14 20:00:00")
        self.assertEqual(parse_qs(urlparse(fetch.call_args.args[0].full_url).query)["after"], ["cursor"])

    def test_cross_tenant_is_failure(self):
        with patch("linkd_runtime.source_alerts._open_query", return_value=self.page([self.alert(bk_tenant_id="other")])), self.assertRaises(ValueError):
            ActiveAlerts("http://linkd.invalid", "token")("tenant", "source", {})

    def test_budget_does_not_return_partial_result(self):
        for limits in ({"max_bytes": 1}, {"max_items": 0}, {"max_pages": 1}):
            with self.subTest(limits=limits), patch("linkd_runtime.source_alerts._open_query", return_value=self.page([self.alert()], "more")), self.assertRaises(ValueError):
                ActiveAlerts("http://linkd.invalid", "token", **limits)("tenant", "source", {})

    def test_query_failure_is_not_empty_success(self):
        with patch("linkd_runtime.source_alerts._open_query", side_effect=TimeoutError()), self.assertRaises(TimeoutError):
            ActiveAlerts("http://linkd.invalid", "token")("tenant", "source", {})

    def test_latest_includes_terminal_alarms(self):
        terminal = self.alert(status="recovered", begin_at="2026-11-14T13:00:00Z")
        with patch("linkd_runtime.source_alerts._open_query", return_value=self.page([self.alert(), terminal])) as fetch:
            result = ActiveAlerts("http://linkd.invalid", "token").latest("tenant", "source")
        self.assertEqual(result["alarm_time"], "2026-11-14 21:00:00")
        self.assertEqual(result["action"], "resolved")
        self.assertNotIn("status", parse_qs(urlparse(fetch.call_args.args[0].full_url).query))

    def test_internal_token_file_rotation(self):
        from pathlib import Path
        import tempfile
        with tempfile.TemporaryDirectory() as directory:
            token = Path(directory) / "token"
            reader = ActiveAlerts("http://linkd.invalid", token_file=str(token))
            seen = []
            def fetch(request, **kwargs):
                seen.append(dict(request.header_items()))
                return self.page([])
            with patch("linkd_runtime.source_alerts._open_query", side_effect=fetch):
                token.write_text("first-jwt\n")
                reader("tenant", "source", {})
                token.write_text("Bearer rotated-jwt\n")
                reader("tenant", "source", {})
            self.assertEqual([headers["Internal-token"] for headers in seen], ["Bearer first-jwt", "Bearer rotated-jwt"])
            self.assertTrue(all("Authorization" not in headers for headers in seen))

    def test_query_refuses_redirects(self):
        from http.server import BaseHTTPRequestHandler, HTTPServer
        import threading
        from urllib.error import HTTPError
        paths = []
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                paths.append(self.path)
                self.send_response(302)
                self.send_header("Location", "/unexpected-target")
                self.end_headers()
            def log_message(self, *_args):
                pass
        server = HTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01})
        thread.start()
        try:
            with self.assertRaises(HTTPError) as raised:
                ActiveAlerts("http://127.0.0.1:%d" % server.server_port, "signed-token")("tenant", "source", {})
            self.assertEqual(raised.exception.code, 302)
            self.assertEqual(len(paths), 1)
        finally:
            server.shutdown()
            thread.join(timeout=2)
            server.server_close()
