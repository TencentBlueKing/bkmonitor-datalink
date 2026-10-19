// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
// Package aegisv2 接收 Aegis V2 数据，并按 BK-RUM 语义约定提交 OTel Traces。
//
// # 包边界
//
//   - http.go：路由注册、请求读取、Record 校验发布和 HTTP 响应。
//   - whitelist.go：Aegis SDK 采样控制接口，独立于数据转换规则。
//   - decode.go：JSON/Protobuf 与 Aegis/OTLP 分流、JSON 前导字符兼容。
//   - internal/aegis：输入解析、批次状态、事件分发、公共上下文和各事件映射。
//   - internal/rum：来源无关的 Span 名称、Kind、公共缺省规则和结构校验。
//
// 调用链：ExportCollect -> decodeCollect -> aegis.ConvertJSON ->
// Collector Validate/Publish。转换包不访问 HTTP，也不发布 Record。
// OTLP JSON/Protobuf Traces 解码后直接返回，不应用 Aegis 的缺省或 BK-RUM 校验。
// Protobuf 不做 JSON 前导字符清理；损坏的 Aegis 报文不会回退成 OTLP。
//
// # 输出与兼容边界
//
// /collect 对 envelope 和 d2 各解析一次，只校验、发布 RecordTraces。
// 不创建 Metrics/Logs，也不将 Web Vitals 派生为直方图。
// 仅输出明确映射的 BK-RUM 字段；未映射字段丢弃，不透传 aegisv2.ext.*。
// 协议映射与已知缺失字段详见 PROTOCOL_MAPPING.md、Aegis_v2_to_bk-rum.md
// 和 internal/aegis 的包文档。
//
// rum 的缺省规则沿用现有兼容策略：service.name=unknown_service；有
// session.id 时 session.type=user；未知 resource.type=other；错误事件默认
// error.handled=false；未设置结果时 outcome.type=success。显式值不被覆盖。
// 未知采样率、Replay、生命周期时间点、计数及阶段耗时均保持缺失，不统一补零。
// rum 校验转换器可保证的名称、Kind、标识、时间顺序、Outcome 和 Vital 值；
// 它不是浏览器 SDK 全量必填字段校验器，Aegis 无法提供的字段不据此拒收。
//
// # 维护与验证
//
// 修改来源报文看 internal/aegis/payload.go，修改事件映射看 aegis 下对应事件文件，
// 修改批次与公共上下文看 aegis/convert.go、aegis/context.go。
// 目标协议约束与公共缺省规则由 internal/rum/schema.go 维护。
// 测试与实现同包同名；testdata/resource.json 同时用于 HTTP 与独立转换测试。
//
// 验证命令：go test ./receiver/aegisv2/...；decode_bench_test.go 验证接入解码
// 和转换链路的分配与吞吐，包含大量未映射字段输入及未知类型回退场景。
package aegisv2
