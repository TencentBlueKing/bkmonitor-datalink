# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""多线程常驻执行进程；不调度来源、不重试，超时后销毁执行环境。"""

import multiprocessing
import json
import os
import queue
import signal
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor

from .errors import TemporaryError
from .executor import Executor


class ExecutorBusy(RuntimeError):
    """执行槽忙；调用方应立即返回繁忙，而不是排队。"""


def _encode_result(result, limit):
    """在进入进程管道前限制 UTF-8 字节，避免先复制完整超大 JSON。"""
    body = bytearray()
    for chunk in json.JSONEncoder(ensure_ascii=False, allow_nan=False).iterencode(result):
        encoded = chunk.encode()
        if len(body) + len(encoded) > limit:
            raise OverflowError("execution result exceeds byte budget")
        body.extend(encoded)
    return bytes(body)


def _serve(incoming, factory, max_result_bytes, threads, ready, redirect_output):
    if redirect_output:
        sys.stdout = sys.stderr
    if os.name == "posix":
        os.setsid()
    try:
        executor = factory()
    except BaseException as error:
        # 启动异常也可能携带凭据；父进程只接收分类，不打印插件 traceback。
        ready.send_bytes(type(error).__name__.encode())
        ready.close()
        return
    ready.send_bytes(b"ready")
    ready.close()
    def run(request, reply):
        try:
            result = executor.execute(request)
            encoded = _encode_result(result, max_result_bytes)
        except Exception as error:
            result = {
                "version": 1,
                "request_id": request.get("request_id"),
                "bk_tenant_id": request.get("bk_tenant_id"),
                "source_id": request.get("source_id"),
                "error": {"code": "temporary_error" if isinstance(error, TemporaryError) else "plugin_error", "type": type(error).__name__},
            }
            # 错误信封独立于业务结果预算，不携带插件正文；不能因为超限而丢失失败反馈。
            encoded = json.dumps(result).encode()
        try:
            reply.send_bytes(encoded)
        finally:
            reply.close()

    with ThreadPoolExecutor(max_workers=threads) as pool:
        while True:
            request, reply = incoming.get()
            pool.submit(run, request, reply)


class ProcessExecutor:
    """一个实例对应一个进程及固定线程槽；execute 可按需启动或重建，停流后调用 close。

    spawn 避免继承 HTTP 线程与连接。队列仅承载当前调用，不积压业务任务；
    超时销毁整组子进程，防止旧插件继续产生副作用。不保证撤销已完成的外部请求。
    """

    def __init__(self, *, timeout=30, factory=Executor, max_result_bytes=8 * 1048576, threads=1, redirect_output=False):
        if timeout <= 0 or max_result_bytes <= 0 or threads < 1:
            raise ValueError("positive execution timeout and result budget required")
        self.redirect_output = redirect_output
        self.threads = threads
        self.slots = threading.BoundedSemaphore(threads)
        self.active = 0
        self.timeout = timeout
        self.factory = factory
        self.max_result_bytes = max_result_bytes
        self.context = multiprocessing.get_context("spawn")
        self.lock = threading.Lock()
        self.process = None
        self.closed = False

    def start(self, cancel_event=None):
        if cancel_event is not None and cancel_event.is_set():
            raise InterruptedError("plugin initialization canceled")
        if self.closed:
            raise RuntimeError("executor is closed")
        if self.process is not None:
            return
        self.incoming = self.context.Queue(maxsize=self.threads)
        receiver, sender = self.context.Pipe(duplex=False)
        self.process = self.context.Process(target=_serve, args=(self.incoming, self.factory, self.max_result_bytes, self.threads, sender, self.redirect_output))
        try:
            self.process.start()
            sender.close()
            deadline = time.monotonic() + self.timeout
            while not receiver.poll(min(0.05, max(0, deadline - time.monotonic()))):
                if cancel_event is not None and cancel_event.is_set():
                    raise InterruptedError("plugin initialization canceled")
                if time.monotonic() >= deadline:
                    raise TimeoutError("plugin initialization deadline exceeded")
            status = receiver.recv_bytes()
            if status != b"ready":
                raise RuntimeError("plugin initialization failed: " + status.decode())
        except BaseException:
            if self.process.pid is not None:
                self._terminate()
            else:
                self.incoming.close()
                self.process.close()
                self.process = None
            raise
        finally:
            receiver.close()
            sender.close()

    def execute(self, request, cancel_event=None):
        if cancel_event is not None and cancel_event.is_set():
            raise InterruptedError("plugin execution canceled")
        if not self.slots.acquire(blocking=False):
            raise ExecutorBusy("executor is busy")
        receiver, sender = self.context.Pipe(duplex=False)
        registered = False
        deadline = time.monotonic() + self.timeout
        try:
            self.lock.acquire()
            try:
                self.start(cancel_event)
                if cancel_event is not None and cancel_event.is_set():
                    raise InterruptedError("plugin execution canceled")
                process = self.process
                self.active += 1
                registered = True
                self.incoming.put_nowait((request, sender))
            finally:
                self.lock.release()
            while True:
                if cancel_event is not None and cancel_event.is_set():
                    with self.lock:
                        if self.process is process:
                            self._terminate()
                    raise InterruptedError("plugin execution canceled")
                if receiver.poll(min(0.05, max(0, deadline - time.monotonic()))):
                    return json.loads(receiver.recv_bytes())
                with self.lock:
                    if self.process is not process:
                        raise RuntimeError("plugin process was replaced")
                    if not process.is_alive():
                        self._terminate()
                        raise RuntimeError("plugin process exited")
                    if time.monotonic() >= deadline:
                        self._terminate()
                        raise TimeoutError("plugin execution deadline exceeded")
        finally:
            receiver.close()
            sender.close()
            if registered:
                with self.lock:
                    self.active -= 1
            self.slots.release()

    def _terminate(self):
        if self.process is None:
            return
        process = self.process
        if os.name == "posix":
            # 主执行进程可能先退出，进程组中的派生进程仍需终止。
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                if process.is_alive():
                    process.kill()
        elif process.is_alive():
            try:
                process.kill()
            except ProcessLookupError:
                # 子进程可能尚未完成 setsid，仍需确保本进程被终结。
                process.kill()
        process.join()
        process.close()
        for channel in (self.incoming,):
            channel.cancel_join_thread()
            channel.close()
        self.process = None

    def close(self):
        """停流后释放进程；活动调用未结束时不等待，避免隐藏排队。"""
        if not self.lock.acquire(blocking=False):
            raise ExecutorBusy("stop active requests before closing executor")
        try:
            if self.active:
                raise ExecutorBusy("stop active requests before closing executor")
            self.closed = True
            self._terminate()
        finally:
            self.lock.release()
