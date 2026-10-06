// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package consume 提供与消息中间件无关的轻量消费运行时。
//
// 运行时只负责有界拉取、并发处理、进程内重试和传输确认。业务 Handler
// 不接触原生消息队列客户端或确认凭据；进程崩溃后的恢复依赖尚未确认的
// Broker 消息重新投递，因此业务处理必须按稳定 Message.ID 保持幂等。
package consume
