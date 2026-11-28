# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""内部同步执行 HTTP 边界；容量耗尽直接拒绝，不创建无界请求线程。"""

import hmac
import json
import logging
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from .errors import TemporaryError
from .process_executor import ExecutorBusy


class RuntimeServer(ThreadingHTTPServer):
    """执行器由装配层注入；CPU 隔离与脚本超时仍需执行进程层提供。"""

    daemon_threads = True

    def __init__(self, address, executor, token, *, concurrency=4, max_body_bytes=1048576, read_timeout=10):
        if not token or concurrency < 1 or max_body_bytes < 1 or read_timeout <= 0:
            raise ValueError("authentication and positive HTTP budgets are required")
        self.executor = executor
        self.token = token.encode()
        self.slots = threading.BoundedSemaphore(concurrency)
        self.max_body_bytes = max_body_bytes
        self.read_timeout = read_timeout
        super().__init__(address, Handler)

    def process_request(self, request, client_address):
        request.settimeout(self.read_timeout)
        if not self.slots.acquire(blocking=False):
            try:
                request.sendall(b"HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
            finally:
                self.shutdown_request(request)
            return
        try:
            super().process_request(request, client_address)
        except BaseException:
            self.slots.release()
            raise

    def process_request_thread(self, request, client_address):
        try:
            super().process_request_thread(request, client_address)
        finally:
            self.slots.release()


class Handler(BaseHTTPRequestHandler):
    """每个连接只执行一次请求，不记录 Authorization、请求正文或插件异常原文。"""

    def do_POST(self):
        if self.path != "/execute":
            self.respond(404, {"error": "not_found"})
            return
        supplied = self.headers.get("Authorization", "").encode()
        if not hmac.compare_digest(supplied, b"Bearer " + self.server.token):
            self.respond(401, {"error": "unauthorized"})
            return
        try:
            if self.headers.get("Transfer-Encoding"):
                raise ValueError("unsupported transfer encoding")
            length = int(self.headers["Content-Length"])
            if length < 0:
                raise ValueError("negative content length")
        except (ValueError, TypeError):
            self.respond(400, {"error": "invalid_length"})
            return
        if length > self.server.max_body_bytes:
            self.respond(413, {"error": "request_too_large"})
            return
        try:
            raw = self.rfile.read(length)
            if len(raw) != length:
                raise ValueError("incomplete body")
            request = json.loads(raw)
            if not isinstance(request, dict):
                raise ValueError("expected object")
        except (ValueError, TimeoutError):
            self.respond(400, {"error": "invalid_body"})
            return
        try:
            result = self.server.executor.execute(request)
        except ExecutorBusy:
            self.respond(503, {"error": "executor_busy"})
            return
        except TimeoutError:
            self.respond(504, {"error": "execution_timeout"})
            return
        except Exception as error:
            logging.getLogger(__name__).error("module=collector operation=execute error=%s", type(error).__name__)
            code = "temporary_error" if isinstance(error, TemporaryError) else "plugin_error"
            result = {"version": 1, "request_id": request.get("request_id"), "bk_tenant_id": request.get("bk_tenant_id"), "source_id": request.get("source_id"), "error": {"code": code, "type": type(error).__name__}}
        self.respond(200, result)

    def respond(self, status, value):
        body = json.dumps(value, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.close_connection = True
        self.wfile.write(body)

    def log_message(self, *_args):
        pass
