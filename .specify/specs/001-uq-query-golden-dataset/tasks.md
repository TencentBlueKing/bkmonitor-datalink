# UQ 生产查询 golden 数据集任务分解

## Task 1：协议失败测试

- 定义期望的 case 文件布局和字段。
- 测试缺少 input/route/dependencies/outputs 时失败。
- 测试正式 case 不允许 `captured_downstream`。
- 测试重复 ID、重复形似签名和敏感内容失败。

验收：新测试在旧实现上按预期失败。

## Task 2：协议加载与安全门禁

- 实现扫描式 loader。
- 实现严格 metadata 校验和跨文件安全扫描。
- 更新现有 VM case 为完全脱敏的新协议。

验收：Task 1 测试通过，原有泄密样本被拒绝。

## Task 3：多 output 捕获与归一化

- 定义统一 downstream output 模型。
- 捕获并规范化 BKBase、ES、InfluxDB 请求。
- 稳定排序但保留重复项。
- 添加一进多出和重复保留测试。

验收：新测试先失败后通过。

## Task 4：route/dependency fixture runner

- 从 route.json 装载空间、结果表、data label 和 storage。
- 从 dependencies.json 返回最小 VM、BKSQL、ES、InfluxDB 响应。
- 根据 request path 调用真实 handler。

验收：case 不需要 Go 列表登记，断开实时依赖仍能执行。

## Task 5：生产样本转正

- VM、ES、Doris、TSpider、HDFS、InfluxDB 候选按形似去重，并逐条标记 output 是否直接来自生产证据。
- 对每个代表完成一致占位符脱敏。
- 用 runner 回放并校准最小 fixture。

验收：每条 case 的 actual outputs 与 expected outputs 一致。

## Task 6：跨窗收敛记录

- 记录各时间窗抽样数、可关联数、已有/新增签名数。
- 对尚未收敛或无可观测日志的分类明确标记。
- 补齐仍然新增的代表 case。

验收：已收敛分类最后一窗无新签名；其他分类有明确后续条件。

## Task 7：验证与 PR 交付

- 运行目标测试、相关 UQ tests、gofmt 和敏感信息检查。
- 审查差异，只保留目标相关改动。
- 提交并推送到 PR #1411 head 分支。

验收：PR 最新 head 包含数据集和 runner，检查结果可复现。
