// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package cleaner 监督静态 EventSource 的消息消费流程，将 RawEventMessage 确定性地转换为 Event。
// 核心 Runtime 只依赖 consume.Session，并按 lane 独立完成连续批量持久化、Mailbox 入队和消息确认；
// Kafka 只是当前首个装配适配器。
package cleaner
