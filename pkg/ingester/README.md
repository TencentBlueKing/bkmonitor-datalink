# Event Ingester

故障自愈事件源接入模块。用于接收及拉取来自第三方系统的事件数据，并推送到后端队列


## 编译

```bash
$ make
```


## 命令

### 1. 启动


```bash
$ ingester run
```

#### 仅启动 Poller

```bash
$ ingester run poller
```

#### 仅启动 Receiver
```bash
$ ingester run receiver
```


参数说明

```bash
Run ingester server for receiving and polling event data

Usage:
  ingester run [all,receiver,poller] [flags]

Flags:
  -b, --bind-address string   Address to be listened
      --debug                 Use debug mode
  -h, --help                  help for run
  -p, --pid string            pid file (default "ingester.pid")
      --safety                start safety

Global Flags:
  -c, --config string   config file (default is ./ingester.yaml) (default "ingester.yaml")
```

### 2. 查看版本

```bash
$ ingester version
```

## 配置

```yaml
consul:  # consul 配置
  address: 127.0.0.1:8500   # consul 地址
  event_buffer_size: 32     # 缓冲区大小
  data_id_path: "metadata"  # data_id 配置的 key 前缀
http:   # http 事件接收器配置
  bind_address: "127.0.0.1:9001"   # 绑定地址，与 host+port 二选一配置
  host: 127.0.0.2                  # 绑定域名
  port: 8000                       # 绑定端口
  debug: false                     # 是否打开 gin 调试模式
logging:
  level: debug  # 日志级别
  output: file  # 输出目标，可选 stdout, file
  options:
    file: ingester.log  # 日志文件路径
    maxage: 10          # 最多保留天数
    maxsize: 500        # 单文件最大MB
    maxbackups: 10      # 最大保留文件个数
    compress: true      # 是否压缩历史日志
```