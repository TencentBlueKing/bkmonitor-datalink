#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

# 测试单pipeline的执行效率
list="1001 1007 1013 1011 1100004 1500511 1500870 1500959"

# transfer的批次配置
size_list="100"
# transfer单批次的等待时间
interval_list="0.1s"
# rt表并发数
multi_num_list="1 5 10 20 30"
# transfer程序位置
cmd="../transfer"
chmod +x $cmd

# transfer指标地址
http_addr="http://10.0.0.1:10202/"
token="bk_bkmonitorv3:383660fb-2e70-44f0-a0ac-a7cf7540d44c"

# 遍历配置项，逐个测试
for interval in $interval_list;
do
for size in $size_list;
do
for multi_num in $multi_num_list;
do
# 从current文件里获取序号，该序号每次测试递增，以切换consumer group
current=`cat current`
current=$(($current+1))
echo $current > current
# 将配置文件覆盖，backup文件里做了一些处理，以便脚本写入动态参数
cp ../transfer_backup.yaml ../transfer.yaml
echo "kafka.consumer_group_prefix: \"bkmonitorv3_transfe$current\"" >>../transfer.yaml
echo "pipeline.backend.buffer_size: $size" >>../transfer.yaml
echo "pipeline.backend.flush_interval: $interval" >>../transfer.yaml

# 替换配置
sed -i "s/\"multi_num\":__multi_num__/\"multi_num\":$multi_num/g"  config/*

# 逐个dataid测试
for i in $list;
do
# 配置consul，选择测试的dataid
./transfer_test.py $i

# 启动进程
$cmd run --config ../transfer.yaml &
pid=$!
sleep 10
# 查询性能信息，可选
curl -u"${token}" ${http_addr}debug/pprof/profile?seconds=60 > pprof/pprof_${interval}_${size}_${multi_num}_${i}.log

# 等待一定时间后kill掉进程，开始下一项测试
sleep 300
kill $pid
done
# 恢复配置
sed -i "s/\"multi_num\":$multi_num/\"multi_num\":__multi_num__/g"  config/*
done
done
done
rm ../transfer.yaml