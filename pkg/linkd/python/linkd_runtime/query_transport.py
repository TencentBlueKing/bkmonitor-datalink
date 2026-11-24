# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""宿主只读查询的 HTTP 连接边界，不跟随可能携带内部认证头的重定向。"""

from urllib.error import HTTPError
from urllib.request import build_opener, HTTPRedirectHandler


class _NoRedirect(HTTPRedirectHandler):
    """查询使用自定义认证头，禁止重定向携带凭据到其他地址。"""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def open_query(request, *, timeout):
    try:
        return build_opener(_NoRedirect).open(request, timeout=timeout)
    except HTTPError as error:
        # 错误响应同样持有连接；执行边界只使用错误分类，不读取其业务正文。
        error.close()
        raise
