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

## 字段类型专项

先从 Doris information_schema 取 COLUMN_TYPE、可空性、精度和 scale，再对照 field_map 及 UQ JSON。不能按字段名推断类型。每条夹具用不可重复的事件键定位，按该键及事件时间固定排序。每类至少准备正常值、边界值、SQL NULL 三组；需要验证缺字段时，再加一条缺失该叶子的记录。保留输入真值，用同一请求取 BKBase 和 Doris 的原文、字段描述及聚合。三方比较可发现两条链路同时取整或截断的情况。

| 编号 / 类型 | 输入样本 | 查询操作 | 预期及容易漏掉的差异 |
|---|---|---|---|
| FT01 BOOLEAN | false、true、NULL | 投影，按值过滤，分别计数 | 执行器值为整数 0、1、nil；核对 JSON 类型，false 与 0 的 Python 相等运算不足以判定协议一致。 |
| FT02 TINYINT / SMALLINT / INT | -128/127、-32768/32767、-2147483648/2147483647，另有 0 | 各取最小值等值命中；范围跨 0；升序；min/max | 数值排序、符号和端点正确。相邻整数分别匹配不同事件。 |
| FT03 BIGINT | 9007199254740992 和 9007199254740993、最小有符号值 | 明细、两次等值、倒序 SearchAfter | 两个相邻值可区分；翻页不跳过第二行；从原始 JSON 精确读整数，不能先转 double。 |
| FT04 无符号整数 / LARGEINT | 最大 uint64、最大有符号 128 位整数 | 原文、字段类型、相邻值过滤 | UNSIGNED BIGINT 驱动文本转 uint64；LARGEINT 若驱动返回文本，逐字符保留。须有真实驱动类型证据才宣称物理列通过。 |
| FT05 FLOAT / DOUBLE | -125、0、0.125、0.1 和相近小数 | 原文、范围、avg/min/max、PromQL 窗口 | 精确可表示值应相等。不能用宽泛容差掩盖大整数或日期错误；非精确值记录输入精度、绝对和相对误差。 |
| FT06 DECIMAL(p,s) | -12.5000、0.0001、9007199254740993.00 | 投影、精确过滤、sum 后与写入真值对照 | 当前执行器转 float64，最后一项会舍入为 9007199254740992。区分「与 BKBase 展示相同」及「保留十进制精度」，后者不能由前者推出。 |
| FT07 CHAR / VARCHAR / STRING / TEXT | 空串、尾空格、中文和 emoji、纯数字字符串、引号和反斜杠 | 投影、等值；已分词字段另测短语和词项 | 保留字符、转义和字符串身份；CHAR 的填充按实际数据库读取基准验收。空串、字符串 "null" 和 SQL NULL 分开判定。 |
| FT08 DATE / DATETIME / TIMESTAMP | UTC+8 零点、前一毫秒、微秒尾数 123456 | 跨日范围、边界等值、按分钟分桶、原文 | DATE 是日期串；驱动返回 time.Time 的时间戳按 Unix 毫秒返回，微秒尾数按协议截到毫秒。VARCHAR 时间串另列。 |
| FT09 JSON / VARIANT | 对象、空对象、数组、空数组、JSON null、深层数字和字符串、大小写不同的键 | 读取根、投影叶子、按叶子等值、缺失叶子、混合版本查询 | 区分执行器的 JSON 文本与 UQ 展开的叶子；缺键、显式 NULL、字符串 "null" 单独计数，数组元素顺序不能打乱。 |
| FT10 ARRAY / MAP / STRUCT | [1,null,2]、空数组、含嵌套值的 map、具名 struct | 根列读取、元素或成员过滤（依实际接口支持）、旧表缺列的 UNION | 数组顺序、元素类型、NULL 位置和对象成员均要检查。若只取得驱动序列化文本，仅验收文本保真。 |
| FT11 NULL 与聚合 | 四行值依次为 NULL、0、1、1；字符串另用 NULL 和空串 | 原文、count(*)、count(v)、sum(v)、avg(v) | 四行均保留；非空数 3，总和 2，平均值 2/3。全 NULL 组另记是否为空组及 API 填充值。 |
| FT12 跨列类型演进 | 历史 INT→BIGINT，旧表无新列，同一 VARIANT 叶子一张为字符串、一张为数值 | 跨切换点投影、分组、分页，分别查两段作 oracle | 比对 UNION 列位置、对缺列的处理、来源和行数；不能因类型不同静默丢整段数据。 |
| FT13 特殊列 | 若接入声明 IP、二进制、BITMAP/HLL/QUANTILE_STATE 等专用列 | 先获取 field_map，再按产品契约读取或执行支持的聚合 | 对不开放的原文列验证错误/排除契约。没有相应接入或产品支持声明时单列待确认。 |

在线字段扫描工具读取物理列定义，再对两个后端的同一页逐行、逐字段比较。每列输出 physical、types、nonnull、null、missing 与 equal；根列两侧都缺失时 equal 为 true，只有 nonnull 大于零才有非空值证据。空页、取不到物理元数据或没有直查游标的表不计通过。

```sh
python3 field_types.py --env "$ENV_ID" --tenant "$TENANT" --biz "$BIZ" \
  --target "$UQ_TARGET" --table "$RT" --start "$START" --end "$END" --limit 10
```

离线执行 `go test ./tsdb/doris -run TestFieldTypeWireContract -count=1`（工作目录 pkg/unify-query）。24 个驱动输入用例各返回一个非空值和一个 SQL NULL，检查字段存在性、精确 Go 类型、值和 schema。上述过滤、聚合、驱动实际暴露的类型名及跨版本行为仍需真实查询验证。
