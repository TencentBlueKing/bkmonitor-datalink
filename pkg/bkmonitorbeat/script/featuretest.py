# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

# coding: utf-8

import html
import sys
import argparse
import select
import logging
import json
from subprocess import Popen, PIPE

try:
    from socketserver import BaseRequestHandler, TCPServer, UDPServer
except ImportError:
    from SocketServer import BaseRequestHandler, TCPServer, UDPServer
try:
    from http.server import BaseHTTPRequestHandler, HTTPServer
except ImportError:
    from BaseHTTPServer import BaseHTTPRequestHandler, HTTPServer

logging.basicConfig(level=logging.DEBUG, stream=sys.stdout)
logger = logging.getLogger()


# 返回文本的tcp服务
class EchoTCPServer(BaseRequestHandler):
    def handle(self):
        data = self.request.recv(128)
        logger.info("recv %s from %s by tcp", data, self.client_address)
        self.request.send(data)


# 返回文本的udp服务
class EchoUDPServer(BaseRequestHandler):
    def handle(self):
        data, sock = self.request
        logger.info("recv %s from %s by udp", data, self.client_address)
        sock.sendto(data, self.client_address)


# EchoHTTPServer 返回文本的http服务
class EchoHTTPServer(BaseHTTPRequestHandler):
    def do_method(self):
        logger.info("recv %s from %s by http", self.path, self.client_address)
        self.send_response(200)
        self.end_headers()
        self.wfile.write(html.escape(self.path.encode("utf-8")))

    do_GET = do_method
    do_POST = do_method
    do_PUT = do_method
    do_DELETE = do_method
    do_HEAD = do_method
    do_PATCH = do_method


def parse_address(addr):
    host, _, port = addr.partition(":")
    return (host or "127.0.0.1", int(port))


class ServerManager(object):
    def __init__(self, servers):
        self.servers = servers

    def poll_and_do(self, timeout=0.5):
        rs, _, _ = select.select(self.servers, [], [], timeout)
        for server in rs:
            server._handle_request_noblock()

    def shutdown(self):
        for s in self.servers:
            s.server_close()


def main():
    parser = argparse.ArgumentParser()
    # 执行命令
    parser.add_argument("commands", default=["sleep", "30"], nargs="*")
    # udp监听地址
    parser.add_argument("-u", "--udp-address", default="127.0.0.1:9201")
    # tcp监听地址
    parser.add_argument("-t", "--tcp-address", default="127.0.0.1:9202")
    # http监听地址
    parser.add_argument("-s", "--http-address", default="127.0.0.1:9203")
    # 轮询间隔
    parser.add_argument("-i", "--poll_interval", default=0.5, type=float)
    # 模拟运行
    parser.add_argument("--dry-run", default=False, action="store_true")
    args = parser.parse_args()

    # 启动http，tcp，udp服务
    manager = ServerManager([
        UDPServer(parse_address(args.udp_address), EchoUDPServer),
        TCPServer(parse_address(args.tcp_address), EchoTCPServer),
        HTTPServer(parse_address(args.http_address), EchoHTTPServer),
    ])
    manager.poll_and_do(args.poll_interval)

    # 启动子进程运行命令
    process = Popen(args.commands, shell=False, stdout=PIPE)
    while process.poll() is None:
        manager.poll_and_do(args.poll_interval)
    manager.shutdown()

    if process.poll() != 0:
        sys.exit(-abs(process.poll()))

    # 判断命令行结果是否含available
    if not args.dry_run:
        fails = 0
        for i in process.stdout:
            info = json.loads(i)
            if not info.has_key("available"):
                pass
            elif info["available"] < 1:
                fails += 1
                print(json.dumps(info, indent=2))

        sys.exit(fails)


if __name__ == "__main__":
    main()
