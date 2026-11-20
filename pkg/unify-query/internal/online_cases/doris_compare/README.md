# Doris 直查验收用例

用同一 UQ 实例、租户、业务、逻辑 RT 和绝对时间窗对照 BKBase 与 Doris。以下用例与具体部署无关。先选定有两种路由的 RT，在已停止写入或迟到数据影响可控的时间窗内取样。日志明细至少准备 6 条，包含相同时间戳、不同唯一键的数据。另准备数值字段、低基数维度、明确有倒排分词的文本、混合类型或缺失的对象叶子。每项记录实际具备的条件。

## 如何运行

使用已授权的 bkmonitor-admin CLI 和 Python 3，替换实际参数。起止时间为 Unix 秒。主脚本使用标准日志字段 dtEventTimeStamp、__unique_key__、bk_host_id、cloudId、gseIndex、log。没有这些字段的自定义表，按下表单独选择适配的用例。

```sh
python3 verify.py --env "$ENV_ID" --tenant "$TENANT" --biz "$BIZ" \
  --target "$UQ_TARGET" --table "$RT" --metric "$PROM_METRIC" \
  --start "$START" --end "$END" --second-table "$SECOND_RT" > results.jsonl
```

双路由用例传 false / true，默认兼容用例另测无 flag。用 check / check_promql 核实选路，原文响应再核对 result_table_options 的物理目标标识。结构化和 PromQL 的路由预览是独立请求，需要执行级归因时按实查 trace 查日志。两个后端均返回空只算未验证，除非用例专门验证零命中。结果逐行输出，包含两侧 trace、状态和差异摘要；FAIL 或 ERROR 会返回非零状态。脚本中 scroll 按原参数连续调用，会推进服务端测试游标，重复执行前应等待旧会话到期。

## 验收矩阵

| 场景 | 构造与断言 |
|---|---|
| 选路和默认兼容 | 双配置 RT 分别不传、传 false、传 true。前两者返回相同逻辑 RT 的数据；true 的物理目标匹配授权路由。另测仅 BKBase、仅物理表以及路由缺失。 |
| 原文、投影和排序 | 全字段与指定列；按时间和唯一键升降序；比较行序、字段名、值、类型和 total。另核对投影是否符合该 API 的约定。 |
| 筛选和全文 | 测数值与字符串精确匹配、AND/OR/NOT、通配符、词项和短语；从同窗已知样本取词。用明确不存在的值验证空结果。已知分词字段核对 field_map 的 is_analyzed。可重复传 --lucene 追加表达式。 |
| 字段发现 | 对比 field_map 的路径、类型、分词和可聚合标记；tag_values 对同一低基数维度比较完整值集合。 |
| 嵌套和类型 | 精确筛选点分叶子；另构造 VARIANT 对象/数组、NULL 与缺字段、大小写别名、大整数和小数。比较返回类型和值，不将空串、NULL 和缺字段等同。 |
| 普通分页 | 一次取 6 条作为同模式基准；取 3 条、跳过 3 条后再取 3 条，逐条比较。稳定排序必须包含唯一键。 |
| SearchAfter | 单独用 SearchAfter 模式取 6 条作为基准。两后端分别完整回传各自游标，将第二页与基准后半段比较；切换 flag 续页仍用首屏后端。旧版本游标、丢失唯一键后的 offset 降级另测。 |
| 滚动下载 | 两条独立会话，各自保持 Header 与请求体，至少连续取两页。业务字段、返回顺序、total 对齐；忽略后端专属 __table_uuid，页间不能重读同一唯一键。全量导出另选小表遍历到 done。 |
| 聚合与趋势 | 结构化 ts 查询 count/sum/avg/max/min 的分钟桶，按低基数维度分组；核对序列标签、类型、时间和值。另测空桶、整分钟边界、时区与迟到数据。 |
| 多表与历史 | 两个 RT，开启/关闭合并，各核对两后端路由与原文。再用真实 data label 和发生过轮转的 RT 跨切换点查询，校验 UNION 成员、历史物理表、混合 ES/Doris、同集群不同表及跨集群游标；多 reference 不等同于单 reference 的物理 UNION。 |
| 原生 PromQL | 用指向 Doris RT 的指标名验证 selector、精确和正则 matcher、range 与 instant、count/sum/avg/min/max/quantile_over_time、sum by、topk、标量乘法、两个表达式相除和阈值过滤。正向样本至少一个点，比较完整序列与采样值并检查 partial。 |
| 指标特有语义 | 有真实 counter/reset 时测 rate/increase/irate，有带 le 的桶时测 histogram_quantile，有成对指标时测 on/group_left。先验证指标类型和标签，再比较窗口边界、缺失序列和结果。 |
| 错误与隔离 | Doris 单分支的 ES 专属 exact_total 应拒绝；另测畸形 flag、失效游标、无权限空间及不可达实例的错误传播。此类场景只在获授权的测试条件中执行。 |

verify.py 覆盖常规在线对照；表中注明的特殊数据、权限、历史及全量下载条件需单独准备。仓库现有 query/structured 分页选路测试和 query_golden_cases 的 Doris 用例可分别回放旧游标、混合顺序、历史分段、UNION、minuteN 和缺字段行为，离线回放结果应单列。
