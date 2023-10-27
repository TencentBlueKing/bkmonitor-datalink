FROM centos:7

VOLUME /data
RUN yum clean all && rm -f /var/lib/rpm/__db* && rpm --rebuilddb
RUN yum install -y iproute strace tcpdump

RUN mkdir -p /data/bkmonitorbeat/config/child_configs
RUN mkdir -p /data/bkmonitorbeat/logs
RUN mkdir -p /data/hostid && mkdir -p /data/pid && mkdir -p /data/logs && mkdir -p /data/data
COPY bkmonitorbeat /data/bkmonitorbeat/bkmonitorbeat
RUN chmod +x /data/bkmonitorbeat/bkmonitorbeat
CMD /data/bkmonitorbeat/bkmonitorbeat -container -c /data/bkmonitorbeat/config/bkmonitorbeat.conf -disable-normalize
