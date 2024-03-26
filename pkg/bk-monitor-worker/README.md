# BMW (Bkmonitor-Worker) 

支持任务类型:
* 定时任务
* 异步任务
* 常驻任务

## 常驻任务接入文档

适用于接入具有以下特征的任务：

1. 存在死循环，不会中断或退出
2. 无返回数据

组件：

`RunMaintainer` 常驻任务维护器：用于维护一个常驻任务正常运行，包括失败重试、指标上报等功能。

### 代码接入

比如现在需要接入一个 demo 常驻任务，每五秒打印一次当前时间，运行代码如下：

```go
ticker := time.NewTicker(5 * time.Second)
for {
  currentTime := <-ticker.C
  fmt.Println("当前时间: ", currentTime.Format("2006-01-02 15:04:05"))
}
```

接入第一步，需要实现`Operator`接口，这是常驻任务的标准接口，里面有两个方法。

1. `Start(runInstanceCtx context.Context, errorReceiveChan chan<- error, payload []byte)`
    1. 任务启动接口，也就是主逻辑需要写在这个方法下面
    2. 参数：
        1. `runInstanceCtx`  父 context，**常驻任务的死循环中必须要监听此 context 的 Done 信号来退出方法**，否则可能会存在任务已停止，但是代码仍在运行的情况。
        2. `errorReceiveChan` 接收错误的通道，**如果运行过程中发生了可以预知的错误，需要将 `err` 发送给此通道，然后退出执行**，当发生 `err` 后，常驻任务维护器将会在一定时间后重新尝试启动该任务。（简单来说，此通道就是一个让维护器知道这个常驻任务执行失败了的信号，维护器只有从这个通道接收了消息，才会认为常驻任务执行失败了）
        3. `payload`：任务的参数
2. `GetTaskDimension(payload []byte) string`
    1. 获取常驻任务的维度值，常驻任务维护器会为每一个正在运行的任务上报心跳指标 `daemon_running_task_count`，这个指标有一个维度 `task_dimension`，这个维度的值就是此方法返回的字符串。例如 APM 预计算里面每一个常驻任务都是不同的 DataId，那么此方法就是返回 DataId。
    2. 参数：
        1. `payload`：任务参数

demo 任务实现了两个方法后，代码变成这样：

```go
func (d Demo) Start(runInstanceCtx context.Context, errorReceiveChan chan<- error, payload []byte) {
	
	content := string(payload)
	fmt.Printf("start task instance, payload: %s \n", content)
	if content != "ok" {
		errorReceiveChan <- errors.New("receive abnormal payload")
		return
	}
	
	ticker := time.NewTicker(5 * time.Second)
	for {
		select {
		case currentTime := <-ticker.C:
			fmt.Println("当前时间: ", currentTime.Format("2006-01-02 15:04:05"))
		case <-runInstanceCtx.Done():
			fmt.Println("receive task instance done, return")
			return
		}
	}
}
```

接着只需将任务注册到 `taskDefine` 中，一个常驻任务就全部完成了。

```go
var taskDefine = map[string]OperatorDefine{
	"daemon:demo:demo": {initialFunc: func(ctx context.Context) (Operator, error) {
		return demo.Demo{RootCtx: ctx}, nil
	}},
}
```

> `initialFunc` 方法用于获取一个具体常驻任务的单例。不同任务实例（不同的 payload ）启动时，都调用此单例的 `Start` 方法进行启动。



### 相关API

**启动常驻任务**

接下来启动刚才写的常驻任务：

```bash
curl --location --request POST 'http://127.0.0.1:10211/bmw/task/' \
--header 'Content-Type: application/json' \
--data '{
    "kind": "daemon:demo:demo",
    "payload": {"content": "ok"},
    "options": {
        "queue": "default"
    }
}'
```

成功打印出日志：

```bash
start task instance, payload: map[content:ok] 
当前时间:  2024-03-22 21:58:21
当前时间:  2024-03-22 21:58:26
当前时间:  2024-03-22 21:58:31
当前时间:  2024-03-22 21:58:36
```

模拟发生异常：

```bash
2024-03-22 22:00:23.769 INFO    daemon/maintainer.go:152        Binding(daemon:demo:demo-7b22636f6e74656e74223a226e6f745f6f6b227d <------> macBook-Pro.local-47229-95439a0b3fb74b2384703b029b218366) is discovered, task is started, payload: {"content":"not_ok"}
start task instance, payload: map[content:not_ok] 
2024-03-22 22:00:26.056 WARN    daemon/maintainer.go:203        [RETRY] receive ERROR: receive abnormal payload. Task: daemon:demo:demo-7b22636f6e74656e74223a226e6f745f6f6b227d, retryCount: 1 reloadCount: 0 The retry time of the next attempt is: 2024-03-22 22:00:36.055982 +0800 CST m=+32.348077292, (10.00 seconds later)
```

从日志可以看到，由于 payload 为`not_ok`，导致代码里面发生了异常，发送给 `errorReceiveChan` ，维护器得知执行异常将会在 10 秒重试（如果重试次数较多超过阈值，下次重试时间将会以 2X 增长）

**获取正在运行的常驻任务**

```bash
curl --location --request GET 'http://127.0.0.1:10211/bmw/task/?task_type=daemon'
```

返回正在运行的常驻任务列表：

```json
{
    "code": 0,
    "data": [
        {
            "uni_id": "daemon:demo:demo-7b22636f6e74656e74223a226f6b227d",
            "kind": "daemon:demo:demo",
            "payload": {
                "content": "ok"
            },
            "options": {
                "Retry": 10,
                "Queue": "default",
                "TaskID": "8cafe4d2-0e2d-4117-a57e-f0214c0236d5",
                "Timeout": 0,
                "Deadline": "0001-01-01T00:00:00Z",
                "UniqueTTL": 0,
                "ProcessAt": "2024-03-22T22:04:10.156436+08:00",
                "Retention": 0
            },
            "binding": {
                "worker_id": "macBook-Pro.local-48405-49813d1a140b4aa3accefe0844a9fccd",
                "worker_is_normal": true
            }
        }
    ],
    "message": "ok",
    "result": true
}
```

**删除常驻任务**

```bash
curl --location --request DELETE 'http://127.0.0.1:10211/bmw/task/' \
--header 'Content-Type: application/json' \
--data '{
    "task_type": "daemon",
    "task_uni_id": "daemon:demo:demo-7b22636f6e74656e74223a226f6b227d"
}'
```

**重新启动常驻任务**

```bash
curl --location --request POST 'http://127.0.0.1:10211/bmw/task/daemon/reload' \
--header 'Content-Type: application/json' \
--data-raw '{
    "task_uni_id": "daemon:demo:demo-7b22636f6e74656e74223a226f6b227d"
}'
```

