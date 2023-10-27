FROM centos:7

LABEL maintainer="Tencent BlueKing"

RUN mkdir -p /data/bkmonitor/logs  /data/bkmonitor/config

WORKDIR /data/bkmonitor

ADD bin/bk-monitor-worker /data/bkmonitor/bk-monitor-worker
RUN chmod +x /data/bkmonitor/bk-monitor-worker
