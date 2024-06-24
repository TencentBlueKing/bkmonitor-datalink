# PromQL
## 初识PromQL
### 数据类型
瞬时向量：一组时间序列，每个时间序列包含单个样本，它们共享相同的时间戳。也就是说，表达式的返回值中只会包含该时间序列中的最新的一个样本值。而相应的这样的表达式称之为瞬时向量表达式

区间向量： 一组时间序列，每个时间序列包含一段时间范围内的样本数据。

标量： 一个浮点型的数据值。

字符串：一个简单的字符串值。

### 时间序列过滤器
瞬时向量过滤器：选择指标名称为 http_requests_total 的所有时间序列：http_requests_total，选择指标名称为 http_requests_total，job 标签值为 prometheus，group 标签值为 canary 的时间序列：http_requests_total{job="prometheus",group="canary"}，PromQL 还支持用户根据时间序列的标签匹配模式来对时间序列进行过滤，选择指标名称为 http_requests_total，环境为 staging、testing 或 development，HTTP 方法为 GET 的时间序列：http_requests_total{environment=~"staging|testing|development",method!="GET"}

区间向量过滤器：区间向量与瞬时向量的工作方式类似，唯一的差异在于在区间向量表达式中我们需要定义时间选择的范围，选择在过去 5 分钟内指标名称为 http_requests_total，job 标签值为 prometheus 的所有时间序列：http_requests_total{job="prometheus"}[5m]

### 时间位移操作
查询时间过去 5 分钟的 http_requests_total 值：http_requests_total offset 5m，该操作同样适用于区间向量。以下表达式返回指标 http_requests_total 一周前的 5 分钟之内的 HTTP 请求量的增长率：rate(http_requests_total[5m] offset 1w)。

## 操作符
### 二元运算符
算术二元运算符: 
二元运算操作符支持标量和标量，向量和标量，向量和向量之间的运算，在两个标量之间进行数学运算，得到的结果也是标量，向量和标量之间，这个运算符会作用于这个向量的每个样本值上。例如：如果一个时间序列瞬时向量除以 2，操作结果也是一个新的瞬时向量，是原度量指标瞬时向量的每个样本值除以 2（prometheus_http_requests_total/2），瞬时向量与瞬时向量之间进行数学运算时，过程会相对复杂一点，运算符会依次找到与左边向量元素匹配（标签完全一致）的右边向量元素进行运算，如果没找到匹配元素，则直接丢弃。同时新的时间序列将不会包含指标名称
### 布尔运算符
两个标量之间进行布尔运算，必须提供 bool 修饰符，得到的结果也是标量，瞬时向量和标量之间的布尔运算，这个运算符会应用到某个当前时刻的每个时序数据上，如果一个时序数据的样本值与这个标量比较的结果是 false，则这个时序数据被丢弃掉，如果是 true, 则这个时序数据被保留在结果中（prometheus_http_requests_total > 10），瞬时向量与瞬时向量直接进行布尔运算时，同样遵循默认的匹配模式：依次找到与左边向量元素匹配（标签完全一致）的右边向量元素进行相应的操作，如果没找到匹配元素，或者计算结果为 false，则直接丢弃。如果匹配上了，则将左边向量的度量指标和标签的样本数据写入瞬时向量
### 集合运算符
集合运算，可以在瞬时向量与瞬时向量之间进行相应的集合操作
```
vector1 为 A B C，vector2 为 B C D

and与操作，vector1 and vector2 的结果为：B C
or或操作，vector1 or vector2 的结果为：A B C D
unless排除操作， vector1 unless vector2 的结果为：A
```

### 匹配模式
```
样本：
method_code:http_errors:rate5m{method="get", code="500"}  24
method_code:http_errors:rate5m{method="get", code="404"}  30
method_code:http_errors:rate5m{method="put", code="501"}  3
method_code:http_errors:rate5m{method="post", code="500"} 6
method_code:http_errors:rate5m{method="post", code="404"} 21

method:http_requests:rate5m{method="get"}  600
method:http_requests:rate5m{method="del"}  34
method:http_requests:rate5m{method="post"} 120
```
一对一匹配:
一对一匹配模式会从操作符两边表达式获取的瞬时向量依次比较并找到唯一匹配(标签完全一致)的样本值，在操作符两边表达式标签不一致的情况下，可以使用on(label list)或者ignoring(label list）来修改便签的匹配行为。使用ignoreing可以在匹配时忽略某些便签。而on则用于将匹配行为限定在某些便签之内
```
method_code:http_errors:rate5m{code="500"} / ignoring(code) method:http_requests:rate5m

结果：
{method="get"}  0.04            //  24 / 600
{method="post"} 0.05            //   6 / 120
```
该表达式会返回在过去 5 分钟内，HTTP 请求状态码为 500 的在所有请求中的比例。如果没有使用 ignoring(code)，操作符两边表达式返回的瞬时向量中将找不到任何一个标签完全相同的匹配项

多对一和一对多:
多对一和一对多两种匹配模式指的是“一”侧的每一个向量元素可以与"多"侧的多个元素匹配的情况。在这种情况下，必须使用group修饰符：group_left或者group_right来确定哪一个向量具有更高的基数（充当“多”的角色），多对一和一对多两种模式一定是出现在操作符两侧表达式返回的向量标签不一致的情况。因此需要使用ignoring和on修饰符来排除或者限定匹配的标签列表。
```
method_code:http_errors:rate5m / ignoring(code) group_left method:http_requests:rate5m

结果：
{method="get", code="500"}  0.04            //  24 / 600
{method="get", code="404"}  0.05            //  30 / 600
{method="post", code="500"} 0.05            //   6 / 120
{method="post", code="404"} 0.175           //  21 / 120
```
该表达式中，左向量 method_code:http_errors:rate5m 包含两个标签 method 和 code。而右向量 method:http_requests:rate5m 中只包含一个标签 method，因此匹配时需要使用 ignoring 限定匹配的标签为 code。 在限定匹配标签后，右向量中的元素可能匹配到多个左向量中的元素 因此该表达式的匹配模式为多对一，需要使用 group 修饰符 group_left 指定左向量具有更好的基数。

### 聚合操作
Prometheus 还提供了下列内置的聚合操作符，这些操作符作用于瞬时向量。可以将瞬时表达式返回的样本数据进行聚合，形成一个具有较少样本值的新的时间序列，sum (求和)，min(最小值)，max(最大值)，avg(平均值)，stddev(标准差)，count(计数)，count_value(计算具有相同样本值的元素数量)，bottomk(样本值最小的k个元素)，topk(样本值最大的k个元素)，quantile(分布统计)，这些操作符被用于聚合所有标签维度，或者通过 without 或者 by 子语句来保留不同的维度，without 用于从计算结果中移除列举的标签，而保留其它标签。by 则正好相反，结果向量中只保留列出的标签，其余标签则移除，例如，
如果指标 http_requests_total 的时间序列的标签集为 application, instance, 和 group，我们可以通过以下方式计算所有 instance 中每个 application 和 group 的请求总量
```
sum(http_requests_total) without (instance)
等价于
sum(http_requests_total) by (application, group)
```
## PromQL内置函数

abs(v instant-vector) 返回输入向量的所有样本的绝对值

absent(v instant-vector)，如果传递给它的向量具有样本数据，则返回空向量；如果传递的向量没有样本数据，则返回不带度量指标名称且带有标签的时间序列，且样本值为1
```
prometheus_http_requests_total 有数据
prometheus_http_requests_total666 无数据
absent(prometheus_http_requests_total) => Empty query result
absent(prometheus_http_requests_total666) => {} 1
```

ceil(v instant-vector) 将 v 中所有元素的样本值向上四舍五入到最接近的整数
```
node_load5{instance="192.168.1.75:9100"} => 2.79
ceil(node_load5{instance="192.168.1.75:9100"}) => 3
```

changes(v range-vector) 输入一个区间向量，返回其值在所提供的时间范围内更改的次数作为即时向量。

clamp_max(v instant-vector, max scalar) 函数，输入一个瞬时向量和最大值，样本数据值若大于 max，则改为 max，否则不变
```
node_load5{instance="192.168.1.75:9100"} => 2.79
clamp_max(node_load5{instance="192.168.1.75:9100"}, 2) => 2
```

clamp_min(v instant-vector, min scalar) 函数，输入一个瞬时向量和最小值，样本数据值若小于 min，则改为 min，否则不变
```
node_load5{instance="192.168.1.75:9100"} => 2.79
clamp_min(node_load5{instance="192.168.1.75:9100"}, 3) => 3
```

day_of_month(v=vector(time()) instant-vector) 函数，返回被给定 UTC 时间所在月的第几天。返回值范围：1~31

day_of_week(v=vector(time()) instant-vector) 函数，返回被给定 UTC 时间所在周的第几天。返回值范围：0~6，0 表示星期天。

days_in_month(v=vector(time()) instant-vector) 函数，返回当月一共有多少天。返回值范围：28~31

delta(v range-vector) 的参数是一个区间向量，返回一个瞬时向量。它计算一个区间向量 v 的第一个元素和最后一个元素之间的差值，例如，下面的例子返回过去两小时的 CPU 温度差
```
delta(cpu_temp_celsius{host="zeus"}[2h])
```

floor(v instant-vector) 函数与 ceil() 函数相反，将 v 中所有元素的样本值向下四舍五入到最接近的整数。

hour(v=vector(time()) instant-vector) 函数返回被给定 UTC 时间的当前第几个小时，时间范围：0~23。

idelta(v range-vector) 的参数是一个区间向量, 返回一个瞬时向量。它计算最新的 2 个样本值之间的差值

increase(v range-vector) 函数获取区间向量中的第一个和最后一个样本并返回其增长量，例如，这里通过node_cpu[2m]获取时间序列最近两分钟的所有样本，increase计算出最近两分钟的增长量，最后除以时间120秒得到node_cpu样本在最近两分钟的平均增长率
```
increase(node_cpu[2m]) / 120
```

irate(v range-vector) 函数用于计算区间向量的增长率，但是其反应出的是瞬时增长率。irate 函数是通过区间向量中最后两个两本数据来计算区间向量的增长速率，例如，以下表达式返回区间向量中每个时间序列过去 5 分钟内最后两个样本数据的 HTTP 请求数的增长率
```
irate(http_requests_total{job="api-server"}[5m])
```

label_join() 标签合并，例如，以下表达式返回的时间序列多了一个 foo 标签，标签值为 etcd,etcd-k8s
```
up{endpoint="api",instance="192.168.123.248:2379",job="etcd",namespace="monitoring",service="etcd-k8s"}=> up{endpoint="api",instance="192.168.123.248:2379",job="etcd",namespace="monitoring",service="etcd-k8s"}  1
label_join(up{endpoint="api",instance="192.168.123.248:2379",job="etcd",namespace="monitoring",service="etcd-k8s"}, "foo", ",", "job", "service")=> up{endpoint="api",foo="etcd,etcd-k8s",instance="192.168.123.248:2379",job="etcd",namespace="monitoring",service="etcd-k8s"}  1
```

label_replace() 标签替换

minute(v=vector(time()) instant-vector) 函数返回给定 UTC 时间当前小时的第多少分钟。结果范围：0~59。

month(v=vector(time()) instant-vector) 函数返回给定 UTC 时间当前属于第几个月，结果范围：0~12

predict_linear(v range-vector, t scalar) 函数可以预测时间序列 v 在 t 秒后的值。它基于简单线性回归的方式，对时间窗口内的样本数据进行统计，从而可以对时间序列的变化趋势做出预测。该函数的返回结果不带有度量指标，只有标签列表，
例如，基于过去2小时的可用磁盘空间样本数据，来预测主机4个小时后的可用磁盘空间
```
predict_linear(node_filesystem_free{job="node"}[2h], 4 * 3600)
```

rate(v range-vector) 函数可以直接计算区间向量 v 在时间窗口内平均增长速率，例如，以下表达式返回区间向量中每个时间序列过去 5 分钟内 HTTP 请求数的每秒增长率
```
rate(http_requests_total[5m])
 
结果：
{code="200",handler="label_values",instance="127.0.0.1:9090",job="prometheus",method="get"} 0
{code="200",handler="query_range",instance="127.0.0.1:9090",job="prometheus",method="get"}  0
{code="200",handler="prometheus",instance="127.0.0.1:9090",job="prometheus",method="get"}   0.2
...
```

round(v instant-vector, to_nearest=1 scalar) 函数与 ceil 和 floor 函数类似，返回向量中所有样本值的最接近的整数。to_nearest 参数是可选的,默认为 1,表示样本返回的是最接近 1 的整数倍的值

sort(v instant-vector) 函数对向量按元素的值进行升序排序

sort_desc(v instant-vector) 函数对向量按元素的值进行降序排序

sqrt(v instant-vector) 函数计算向量 v 中所有元素的平方根

aggregation_over_time()
下面的函数列表允许传入一个区间向量，它们会聚合每个时间序列的范围，并返回一个瞬时向量

avg_over_time(range-vector) : 区间向量内每个度量指标的平均值。

min_over_time(range-vector) : 区间向量内每个度量指标的最小值。

max_over_time(range-vector) : 区间向量内每个度量指标的最大值。

sum_over_time(range-vector) : 区间向量内每个度量指标的求和。

count_over_time(range-vector) : 区间向量内每个度量指标的样本数据个数。

quantile_over_time(scalar, range-vector) : 区间向量内每个度量指标的样本数据值分位数，φ-quantile (0 ≤ φ ≤ 1)。

stddev_over_time(range-vector) : 区间向量内每个度量指标的总体标准差。

stdvar_over_time(range-vector) : 区间向量内每个度量指标的总体标准方差
## 在unify-query的API中使用 PromQL
在unify-query模块中，你可以通过其API请求中使用 PromQL 来执行查询，获取实时或瞬时时间数据。这样的功能允许开发者和系统管理员在应用程序中集成 Prometheus 数据，或者使用自动化脚本来获取和处理监控数据
### 基本步骤
1.选择API类型：Prometheus 提供了两种主要的 API 来执行 PromQL 查询：即时查询 (/api/v1/query)：用于执行对某一特定时间点的查询。范围查询 (/api/v1/query_range)：用于执行在一个时间范围内的查询。
构建查询语句：根据需要的监控数据，编写 PromQL 查询语句。
通过unify-query的请求发送查询：使用unify-query的API请求向 Prometheus 的 query API 发起请求，将 PromQL 查询语句作为参数传递。
### 使用即时查询 API
构建一个查询 Prometheus 的即时查询请求：
```
curl --location 'http://127.0.0.1:10205/query/ts/promql' \
--header 'Content-Type: application/json' \
--data '{
    "promql":"{"promql":"bkmonitor:etcd_server_slow_apply_total{bk_biz_id=\"2\"}",
    "start":"1629810830",
    "end":"1629811070",
    "step":"30s",
    "instant":true
}'
```

字段instant来判定该查询是即时查询还是范围查询，instant为true时是即时查询，为false时是范围查询
### 使用范围查询 API
对于范围查询，需要制定开始时间、结束时间及步长：
```
curl --location 'http://127.0.0.1:10205/query/ts/promql' \
--header 'Content-Type: application/json' \
--data '{
    "promql":"rate(bkmonitor:system:cpu_detail:nice{bk_target_ip=\"127.0.0.1\",device_name=\"cpu0\"}[2m]) - bkmonitor:system:cpu_detail:idle{bk_target_ip=\"127.0.0.1\",device_name=\"cpu0\"}",
    "start":"1629806531",
    "end":"1629810131",
    "step":"600s",
    "instant":false
}'
```

这里："device_name=\"cpu0\"}[2m]" 查询过去2分钟的device_name=\"cpu0\"的数据。start 和 end 表示查询时间范围的开始和结束时间，step 表示希望每个数据点的时间间隔。
### 处理返回数据
Prometheus 会返回 JSON 格式的响应，其中包含了查询结果。你需要在你的应用程序或脚本中对这些数据进行解析和处理。例如，一个返回结果可能看起来像这样：
```
{
  "series": [
    {
      "name": "_result0",
      "metric_name": "",
      "columns": [
        "_time",
        "_value"
      ],
      "types": [
        "float",
        "float"
      ],
      "group_keys": [
        "__tmp_prometheus_job_name",
        "bcs_cluster",
        "endpoint",
      ],
      "group_values": [
        "",
        "BCS-K8S-40000",
        "http-metrics",
      ],
      "values": [
        [
          1629810810000,
          537
        ],
        [
          1629810840000,
          537
        ]
      ]
    }
  ]
}
```
响应数据格式：区间向量返回的数据类型 resultType 为 matrix，即时向量返回的数据类型 resultType 为 vector，标量返回的数据类型 resultType 为 scalar，字符串返回的数据类型 resultType 为 string，









