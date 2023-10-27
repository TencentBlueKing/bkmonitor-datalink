# 采集器集成测试环境模拟工具

## 功能
- 定期产生系统异常并恢复，包括OOM， core dump，磁盘空间100%，磁盘只读，日志关键字
- 提供定期切换正常和异常状态切换服务，包括http，icmp，tcp，udp，进程端口监听
- 提供prometheus指标数据，包括http和脚本形式

## 使用
```
test service for beat

Usage:
  test_beat [OPTIONS] [flags]

Flags:
      --disk_ro_path string           disk ro test path (default "/test2")
      --disk_space_path string        disk space test path (default "/test1")
      --exception_interval duration   interval for exceptions (default 10m0s)
      --exec string                   execute once, available: script
  -h, --help                          help for test_beat
      --http_port int                 http listen port (default 50080)
      --http_response string          http response (default "hello world")
      --keyword_path string           keyword test path (default "/tmp")
      --process_tcp_port int          process tcp port (default 50083)
      --process_udp_port int          process udp port (default 50083)
      --prom_port int                 udp listen port (default 50084)
      --single string                 start single exception or service by name, available: coredump,oom,diskspace,diskro,keyword,http,tcp,udp,icmp,process,prom
      --tcp_port int                  tcp listen port (default 50081)
      --tcp_response string           tcp response (default "hello world")
      --udp_port int                  udp listen port (default 50082)
      --udp_response string           udp response (default "hello world")
```