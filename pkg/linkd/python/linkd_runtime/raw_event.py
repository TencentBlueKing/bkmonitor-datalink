# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""Linkd standard Cleaner 接收的 Kafka RawEvent payload。

字段以 bkmonitor-datalink/pkg/linkd/internal/cleaner/raw_event.go 的
standardPayload 为准。RawEventMessage 是 Linkd 消费端构造的传输信封，
不由 KAC 插件创建。
heartbeat 仅作为 KAC 插件输入动作，接入侧消费后不进入 Linkd payload。
"""

import json
import re
from datetime import datetime
from typing import Any, Literal, NotRequired, TypedDict


class Evaluation(TypedDict):
    action: Literal["triggered", "resolved", "closed", "heartbeat"]
    severity: str
    action_reason: NotRequired[str]


class Subject(TypedDict, total=False):
    system: str
    type: str
    id: str
    name: str


class RawEvent(TypedDict):
    """KAC 拉取插件交给 Linkd standard Cleaner 的单条消息体。"""

    fingerprint: str
    bk_tenant_id: NotRequired[str]
    event_id: NotRequired[str]
    title: str
    evaluations: list[Evaluation]
    content: NotRequired[str]
    dimensions: NotRequired[dict[str, str | int | float | bool]]
    values: NotRequired[dict[str, float]]
    subject: NotRequired[Subject]
    occurred_at: NotRequired[str]
    produced_at: NotRequired[str]
    labels: NotRequired[dict[str, str | int | float | bool]]
    extra_data: NotRequired[dict[str, Any]]


def validate_raw_event(event: RawEvent, *, tenant_id: str) -> None:
    """校验发布到 Linkd 的业务事件；KAC 心跳须在此之前消费。"""
    if not isinstance(event, dict):
        raise ValueError("Linkd RawEvent must be a JSON object")
    for field in ("fingerprint", "title"):
        if not isinstance(event.get(field), str) or not event[field]:
            raise ValueError(f"Linkd RawEvent {field} is required")
    if not isinstance(event.get("bk_tenant_id"), str) or event["bk_tenant_id"] != tenant_id:
        raise ValueError("Linkd RawEvent tenant does not match the current tenant")
    if len(event["fingerprint"].encode()) > 128:
        raise ValueError("Linkd RawEvent fingerprint exceeds 128 bytes")
    evaluations = event.get("evaluations")
    if not isinstance(evaluations, list) or not 1 <= len(evaluations) <= 32:
        raise ValueError("Linkd RawEvent evaluations must contain 1 to 32 items")
    seen_severities = set()
    for index, evaluation in enumerate(evaluations):
        if not isinstance(evaluation, dict):
            raise ValueError(f"evaluations[{index}] must be a JSON object")
        action = evaluation.get("action")
        if not isinstance(action, str) or action not in {"triggered", "resolved", "closed"}:
            raise ValueError(f"evaluations[{index}].action is invalid")
        severity = evaluation.get("severity")
        if not isinstance(severity, str) or not severity or severity in seen_severities:
            raise ValueError(f"evaluations[{index}].severity is invalid or duplicated")
        if "action_reason" in evaluation and not isinstance(evaluation["action_reason"], str):
            raise ValueError(f"evaluations[{index}].action_reason must be a string")
        seen_severities.add(severity)
    for field in ("event_id", "content", "occurred_at", "produced_at"):
        if field in event and not isinstance(event[field], str):
            raise ValueError(f"Linkd RawEvent {field} must be a string")
    for field in ("occurred_at", "produced_at"):
        if field not in event:
            continue
        value = event[field]
        try:
            if not re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})", value):
                raise ValueError
            datetime.fromisoformat(value.replace("Z", "+00:00"))
        except ValueError:
            raise ValueError(f"Linkd RawEvent {field} must be a valid timezone-aware RFC3339 time") from None
    if "subject" in event:
        subject = event["subject"]
        if not isinstance(subject, dict) or any(
            key in subject and not isinstance(subject[key], str) for key in ("system", "type", "id", "name")
        ):
            raise ValueError("Linkd RawEvent subject must contain string identity fields")
    if "extra_data" in event and not isinstance(event["extra_data"], dict):
        raise ValueError("Linkd RawEvent extra_data must be an object")
    for field in ("dimensions", "labels"):
        mapping = event.get(field, {})
        if not isinstance(mapping, dict) or any(
            not isinstance(key, str) or not key or value is None or not isinstance(value, (str, int, float, bool))
            for key, value in mapping.items()
        ):
            raise ValueError(f"Linkd RawEvent {field} must be a scalar map")
    values = event.get("values", {})
    if (
        not isinstance(values, dict)
        or len(values) > 256
        or any(
            not isinstance(key, str) or not key or isinstance(value, bool) or not isinstance(value, (int, float))
            for key, value in values.items()
        )
    ):
        raise ValueError("Linkd RawEvent values must be a numeric map with at most 256 items")
    try:
        json.dumps(event, allow_nan=False)
    except (TypeError, ValueError) as exc:
        raise ValueError("Linkd RawEvent must be JSON serializable") from exc
