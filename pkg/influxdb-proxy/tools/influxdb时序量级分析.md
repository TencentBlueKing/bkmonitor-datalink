### 背景

用于分析定位influxdb里时序量级大的库表和维度值基数大的维度，从而进行相应库表的维度优化，进而减小influxdb内存使用，提升influxdb性能

避免series量级过大的官方文档说明：[https://docs.influxdata.com/influxdb/v1.8/concepts/schema_and_data_layout/#avoid-too-many-series](https://docs.influxdata.com/influxdb/v1.8/concepts/schema_and_data_layout/#avoid-too-many-series)



### 分析方式

在influxdb机器上运行下面的shell脚本，允许传1个参数，该参数为需要分析的某个database名，不传参数时则是分析top3时序量级的database，分析的是最近一个小时的时序数据，会在当前目录下生成influxdb_series_result目录和influxdb_series_result.tgz文件，最终分析汇总结果记录在文件influxdb_series_result/sorted_dbseries.txt以及相应的database子目录下，以system数据库为例，则是在文件influxdb_series_result/system/output.txt中，可将influxdb_series_result.tgz文件提供给监控平台研发或运维专家进行更深入的分析

脚本运行示例：./influxdb_series_analyze.sh 或 ./influxdb_series_analyze.sh system



### shell脚本

脚本名为influxdb_series_analyze.sh

```bash
#!/bin/bash
# 在influxdb机器上运行该shell脚本，允许传1个参数，该参数为需要分析的某个database名，不传参数时则是分析top3时序量级的database
# 分析的是最近一个小时的时序数据，会在当前目录下生成influxdb_series_result目录和influxdb_series_result.tgz文件
# 最终分析汇总结果记录在文件influxdb_series_result/sorted_dbseries.txt以及相应的database子目录下
# 以system数据库为例，则是在文件influxdb_series_result/system/output.txt中
# 脚本运行示例：./influxdb_series_analyze.sh 或 ./influxdb_series_analyze.sh system

source /data/install/utils.fc

host=$BK_INFLUXDB_IP
port="8086"
username=$BK_INFLUXDB_ADMIN_USER
password=$BK_INFLUXDB_ADMIN_PASSWORD
database=""
format=""
topdbs=""
homedir="influxdb_series_result"

# 根据influxql查询influxdb
function get() {
  echo "$*"
  if [[ -z $username ]]; then
    echo "influx --host $host --port $port --database $database -precision 'rfc3339' --format $format -execute '$*'" | bash
  else
    echo "influx --host $host --port $port --username $username --password $password --database $database -precision 'rfc3339' --format $format -execute '$*'" | bash
  fi
}

# 分析单个database的series情况
function analyze() {
  mkdir -p $database
  path="$database/measurements.txt"
  get show measurements | grep "measurements," | awk -F',' '{print $2}' >$path

  total=$(wc <"$path" -l)

  outputPath="$database/output.txt"
  echo "measurement,seriesNum,tagsNum" >$outputPath
  n=0
  while read -r l; do
    ((n++))
    dir="$database/$l"
    mkdir -p "$dir"
    seriesPath="$dir/series.txt"
    if [ ! -f "$seriesPath" ]; then
      # 分析最近1小时的series，可根据场景调整该时间范围
      get "show series from \"$l\" where time > now()-1h" >"$seriesPath"
    fi
    seriesNum=$(wc <"$seriesPath" -l)
    tagsPath="$dir/tags.txt"
    if [ ! -f "$tagsPath" ]; then
      awk <"$seriesPath" -F',' '{for(i=1;i<NF;i++){print $i;}}' | grep "=" | awk '{a[$1]++} END {for (i in a){print i, a[i]}}' | sort >$tagsPath
    fi
    tagsNum=$(wc <"$tagsPath" -l)
    tagKeyPath="$dir/tagkey.txt"
    awk <"$tagsPath" -F'=' '{a[$1]++} END {for (i in a){print i, a[i]}}' | sort -n -k 2 -r >"$tagKeyPath"
    echo -e "\n$l,$seriesNum,$tagsNum" >>$outputPath
    cat "$tagKeyPath" >>$outputPath
    echo "measurement: $l, progress: $n / $total"
  done <"$database/measurements.txt"

  grep <$outputPath "," | sort -t ',' -k 2 -nr >"$database/sorted_measurements.txt"
}

# 获取各database时序量级的top情况
function get_top_dbs() {
  database="_internal"
  format="column"
  get "select * from \"database\" where time > now() -1m" >$homedir/dbseries.txt
  echo "database numSeries" >$homedir/sorted_dbseries.txt
  tail -n +5 $homedir/dbseries.txt | awk '{print $2,$5}' | sort -rnk2 | awk '!a[$1]++' >>$homedir/sorted_dbseries.txt
}

rm -rf $homedir $homedir.tgz
mkdir $homedir

get_top_dbs
# 允许传1个参数，该参数为需要分析的某个db名，不传参数时则是分析top3时序量级的db
if [ "$1" != "" ]; then
  topdbs=$1
else
  # 获取top3的db，可根据场景调整该top数
  topdbs=$(tail -n +2 $homedir/sorted_dbseries.txt | head -n 3 | awk '{print $1}')
fi

echo "start at "$(date)
echo -e "prepare analyzing these database:\n$topdbs"
cd $homedir
# 分析db的series情况
for db in $topdbs; do
  database=$db
  format="csv"
  echo -e "\nbegin analyzing database: $db"
  analyze
done

cd ../
tar -zvcf "$homedir.tgz" "$homedir"
echo "end at "$(date)

```


