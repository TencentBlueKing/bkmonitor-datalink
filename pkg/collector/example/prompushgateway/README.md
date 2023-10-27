# pushgateway 接入文档

用户使用 prometheus 原始 SDK 上报即可，不过需要指定蓝鲸的上报端点（$host:$port）以及 HTTP Headers。

```
X-BK-TOKEN=$TOKEN
```

prometheus sdk 库：https://prometheus.io/docs/instrumenting/clientlibs/

## Golang

1. 补充 headers，用于携带 token 信息。定义 Client 行为，由于 prometheus sdk 没有提供新增或者修改 Headers 的方法，所以需要实现 Client interface。
2. 填写上报端点，在 `push.New("$endpoint", name)` 里指定。然后需要将自定义的 client 传入到 `pusher.Client($bkClient{})` 里面。
3. 建议上报时 Grouping 指定 `instance` labels，这样页面上能够以 target 维度归组。

```go
type bkClient struct{}
func (c *bkClient) Do(r *http.Request) (*http.Response, error) {
	r.Header.Set("X-BK-TOKEN", "$TOKEN")	// TOKEN 即在 saas 侧申请的 token
	return http.DefaultClient.Do(r)
}

func main() {
	register := prometheus.NewRegistry()
	register.MustRegister(promcollectors.NewGoCollector())

	name := "demo"
	// 1) 指定蓝鲸上报端点 $bk.host:$bk.port 并指定 grouping instance labels
	pusher := push.New("localhost:4318", name).Gatherer(register).Grouping("instance", "my.host.ip")

	// 2) 传入自定义 Client
	pusher.Client(&bkClient{})

	ticker := time.Tick(15 * time.Second)
	for {
		<-ticker
		if err := pusher.Push(); err != nil {
			log.Println("failed to push records to the server, error:", err)
			continue
		}
		log.Println("push records to the server successfully")
	}
}
```

## Python

1. 补充 headers，用于携带 token 信息。实现一个自定义的 handler。 
2. 填写上报端点，在 `push_to_gateway("$endpoint", ...)` 里指定。然后将自定义的 handler 传入到函数里。
3. 建议上报时 Grouping 指定 `instance` labels，这样页面上能够以 target 维度归组。

```python
from prometheus_client.exposition import default_handler

# 定义基于监控 token 的上报 handler 方法
def bk_handler(url, method, timeout, headers, data):
    def handle():
        headers.append(['X-BK-TOKEN', '$TOKEN'])    # TOKEN 即在 saas 侧申请的 token
        default_handler(url, method, timeout, headers, data)()
    return handle

from prometheus_client import CollectorRegistry, Gauge, push_to_gateway
from prometheus_client.exposition import bk_token_handler

registry = CollectorRegistry()
g = Gauge('job_last_success_unixtime', 'Last time a batch job successfully finished', registry=registry)
g.set_to_current_time()
push_to_gateway('localhost:4318', job='batchA', registry=registry, grouping_key={"instance", "my.host.ip"}, handler=bk_handler)
```
