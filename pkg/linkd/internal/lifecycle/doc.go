// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package lifecycle 负责把尚未处理的 Event 裁决为 Alert 创建、更新或终态转换。
//
// 本包实现可独立测试的应用服务和跨对象部分成功恢复，不负责上游消息恢复。调用方必须通过
// Mailbox 和 lease 保证同一 (bk_tenant_id, event_source_id, fingerprint) 的 Event 串行处理。
package lifecycle
