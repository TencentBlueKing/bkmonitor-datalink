# UQ 生产查询 golden 数据集实施计划

## 1. 采样验证

- 从多个固定历史时间窗按后端各抽取约 20 条 output。
- 在 output 时间点附近反查同 trace 的 input，拒绝多入口或不完整序列。
- 使用 Trace 中的 `build-metadata-query` 信息辅助还原最小 route fixture。
- 只输出结构统计；原始日志留在本地受控缓存。

验证：形成各分类的样本数、唯一关联率、形似签名增量和版本一致性记录。

## 2. 数据协议

- 将 case 拆分为 metadata、request、route、dependencies、expected outputs。
- 取消正式 `captured_downstream` 状态。
- 将来源信息收敛为脱敏证明和不可逆摘要，并区分 production_log、provisional handler_replay 与 post-fix handler replay output。

验证：协议测试拒绝缺文件、未知 backend、重复 ID/签名和不安全内容。

## 3. 离线 runner

- 扫描所有 enabled case。
- 初始化真实 UQ handler 和本地 route fixture。
- 捕获 BKBase、ES、InfluxDB 请求，并返回最小 dependency response。
- 归一化实际 outputs，稳定排序后与 golden 比较。

验证：先用失败测试证明多 output、自动扫描和路由自包含尚不可用，再完成最小实现。

## 4. 初始数据集

- 从唯一关联候选中选择每种新形似的代表。
- 使用统一占位符脱敏 input、route、dependencies 和 output。
- 在固定基线回放；直接关联到生产 output 的 case 标记为 `production_log`，只能由 handler 回放得到 output 的普通 case 必须标记为 provisional，且不计入生产采样收敛；问题修复导致 expected 有意变化时标记为 post-fix handler replay，并保留生产失败形态摘要。

验证：每条正式 case 可单独运行，且断网/无实时路由时结果不变。

## 5. 收敛循环

- 在第二、第三时间窗重复每类 20 条采样。
- 统计每窗新增签名；若仍有新增则补 case 并继续下一窗。
- 对无日志观测能力或零流量类型记录明确缺口。

验证：采样记录中的已收敛分类最后一个窗口新增签名数为 0。

## 6. 交付

- 运行 gofmt、目标测试、相关 service/http 回归和敏感信息扫描。
- 自审实现与公开仓安全边界。
- 将提交推送到 PR #1411 的 head 分支，检查 PR 最新状态。

## 风险与控制

- 日志泄密：原始 evidence 不进入命令输出和仓库；先脱敏再创建 case。
- trace 复用：用窄时间窗和输入/输出计数判定，歧义即丢弃。
- fixture 过拟合：只在真实样本回放失败时增加字段。
- 并发不稳定：outputs 使用稳定排序的 multiset 比较。
- builder 变更被 response matcher 提前阻断：dependency responder 按请求阶段返回通用最小响应，actual 始终先捕获。
- 全局 mock 状态串扰：runner 串行执行并在每个 case 前重置 responder 和请求记录。
