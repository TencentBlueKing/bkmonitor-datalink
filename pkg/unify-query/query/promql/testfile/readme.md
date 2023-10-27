数据采样方案


1.在查询模块所在机器上启用tcpdump,将抓包数据写入文件record.log
本例由于查询模块与influxdb-proxy位于相同机器，所以采集lo网卡数据
tcpdump -w record.log -i lo

2.使用线上运行中的查询模块进行数据查询

3.结束tcpdump，将record.log取回本机，使用wireshark打开，通过追踪http流的方式，获得传输的原始数据