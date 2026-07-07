# NetworkFlow 子配置
# 修改此文件后无需改动主配置即可生效
enabled: false
dataid: 1100025
listeners:
  - "netflow://0.0.0.0:2055"
# UDP 接收协程数，按 CPU 核数调整
workers: 1
# UDP 接收 socket 数
sockets: 1
# 内部 channel 缓冲区大小，0 表示同步模式
queue_size: 0
# 是否阻塞写入，false 表示 channel 满时丢包并触发回调
blocking: false
