FROM centos:7

LABEL maintainer="Tencent BlueKing"

RUN mkdir -p /data/bkmonitor/logs  /data/bkmonitor/config

WORKDIR /data/bkmonitor

ADD bin/bkmonitor_crontask /data/bkmonitor/bkmonitor_crontask
RUN chmod +x /data/bkmonitor/bkmonitor_crontask

ENTRYPOINT ["/data/bkmonitor/bkmonitor_crontask"]
CMD ["-c", "/data/bkmonitor/config/crontask.yaml"]