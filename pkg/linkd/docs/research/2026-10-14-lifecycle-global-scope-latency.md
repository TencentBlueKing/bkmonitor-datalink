# Lifecycle 全局业务匹配耗时排查

日期：2026-10-14。测试环境旧 Worker 版本 `0.1.0-ci.49.kacv3.20261014.1`。

## 根因与现场证据

- 16:38:09–17:38:09，ProcessEvent 平均总耗时约 1.79 s；plan 执行 1373.79 ms，其中 suppression 1260.51 ms。
- 17:24:57–17:39:57，suppression 平均 1634.68 ms；17:39:57 的最近 60 秒平均 3.289 s、P99 9.881 s。
- `linkd-测试抑制` 的空间 `bkcc__524` 在当前租户 metadata_space 中为全局业务。租户有 323 个 cw-biz，完整分页返回约 0.9 MB 业务文档。
- EvaluateConditions 原先每次先 ResolveScope：全局空间分页读取所有业务，再查事件业务、目标身份和条件。缺少 canonical 实例字段的事件仍承担这项成本；同一目标解析还会再次展开全局空间。
- 每来源策略资源装配仅 4 个 HTTP 连接；消息突发时重复分页、JSON 解码/编码与连接等待叠加。现场 suppression 的 not_matched 累计求值均值约 3.15 s，说明正常未命中也在消耗时间。
- Worker 17:29:40 OOMKilled（2 GiB 限额）；重启后约 15 分钟累计分配约 114 GB。此证据与重复展开吻合，但没有现场 CPU profile，不能归因到某一个 JSON 函数。
- 同期抽样 ES CPU 2%–4%，写队列/拒绝为 0；基础 Event/Alert 存储多为几十毫秒。普通 processing_failed 丢弃按当前语义保留，不将业务报错本身作为根因。

## 修复边界

逐事件业务归属改为 ResolveBusinessScope，只查询本次业务 ID；真实全局标记、租户和业务存在性仍从当前权威源读取。缺少目标字段/模型不匹配先返回未命中。完整 selector 集合解析仍保持原有完整性与业务范围语义；不增加跨 Event 成员缓存，不修改消息失败丢弃规则。

## 验证

回归先复现旧实现对缺失/错误模型仍读取范围，修复后要求零范围读取；覆盖全局单业务查询、普通空间匹配、全局标记排除、业务缺失、后端失败、租户隔离与取消。`make check` 全量通过（格式、普通测试、vet、race、golangci-lint、Console、Helm、发行脚本）；另行执行 KAC output、Alert policy、policy diagnostics 三项 Playwright 回归，3 项通过。


## 测试环境发布与复核

- 当前分支 `feat/linkd-dev`：原有 V3 与日志变更为 `80ff8a2f`，本次修复为 `255a906e`；两个提交已推送 GitHub upstream 和 Kingeye remote。
- Linkd 与 Console 的 amd64/arm64 镜像均发布 `0.1.0-ci.49.latency.20261014.1`，四个镜像的版本命令核对源码提交并按 registry digest 回拉通过。
- 构建 VM 外网连接超时，Go 复用已完成测试的本机模块下载缓存，仍使用 Go 1.26.7 和原编译参数；Console 使用本轮构建的 dist/dist-server，生产依赖复用 manifest 完全相同的既有双架构运行时。没有变更依赖版本。
- 18:09:22 通过本地 Chart 包升级 test-bkee5 的 linkd namespace，Helm revision 73；三个角色均为新版本，镜像固定 amd64 digest。Console 在该环境原本关闭，保持关闭。
- Chart 包保留在测试机 `linkd/linkd-0.1.0-ci.49.latency.20261014.1.tgz`。Chart 仓库上传 API 返回 401（当前 Helm 连接只有匿名读取，无上传凭据），未声称发布到 Chart 仓库；环境已实际升级成功。

部署后独立窗口 **18:10:00–18:13:00**（全租户、同一计算口径）：

| 指标 | 修复前截图窗口 | 修复后窗口 |
| --- | ---: | ---: |
| 单 Event 平均总耗时 | 1790 ms | 420.27 ms |
| plan 执行 | 1373.79 ms | 25.03 ms |
| suppression 子步骤 | 1260.51 ms | 1.16 ms |

18:13:00 的最近 60 秒均值 392.74 ms、P99 737.48 ms，完成速率 10.91/s。
18:11:00–18:13:00 的各 15 秒采样：均值 316–393 ms，P99 721–1117 ms。
窗口首个 18:10:00 的 rate/histogram 样本仍向前读取 60 秒，覆盖发布边界，不能用于纯新版本尾延迟比较。

18:13 左右 Lifecycle CPU 228m、内存 63 MiB；新 Pod 观察期间无重启。最近 180 秒日志仍包含 1133 条 processing_failed 丢弃及 1152 条丰富步骤失败，说明普通报错仍在按既有语义处理，已不再引发本次全局范围扫描开销。主要报错为 aggregate_method_missing 等业务输入问题，本次未扩大修复范围。

这是短窗口测试环境验收，不能外推为长期容量承诺；后续可继续从 Event 耗时分布核对真实 I/O 与失败样本。


## 19:47 复查：依赖屏蔽仍存在扫描放大

首轮短窗口只验证了抑制路径，未覆盖后来启用的依赖屏蔽；不能据此判定整个 Lifecycle 高耗时已经解决。
19:32:22–19:47:22 新 Worker 无重启，末端 60 秒平均 2.772 s、P99 28.787 s。
同窗 admission 平均 1345.58 ms，Lifecycle CPU 6123m；system 租户有 485 条活动告警。
启用的 `kac-ceea0a76-8c8f-4cf3-a261-e35f6e828245` 为自定义依赖屏蔽，主策略 146、子策略 148。
`newDependencyBinding` 在判断当前 Event 主/子条件之前调用 admittedMain，逐事件分页读取全租户活动告警，
并逐候选合成 opening 视图、解析业务和目标。日志出现大量 shield_evaluate context deadline exceeded。
这项开销与普通事件处理失败的丢弃语义无关。

补充修复：主/子本地条件均明确不匹配时不扫描；引用/关系保持可能匹配；本地字段不可读沿用正式 Match 的不可用结果，不扫描候选。
主候选本地条件明确不匹配时不做 CMDB I/O；有效视图直接应用补丁，消除 JSON→Alert→JSON 往返。
保持完整选主与实时重读、固定绑定、租户隔离和普通处理报错丢弃，不增加跨事件缓存。
验收要求包含既有 485 条活跃存量与完整新版本 15 分钟窗口，发布后填写实测结果。
