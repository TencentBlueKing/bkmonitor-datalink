FROM centos:7

VOLUME /data
RUN yum clean all && rm -f /var/lib/rpm/__db* && rpm --rebuilddb
RUN yum install -y iproute strace tcpdump

RUN mkdir -p /data/hostid && mkdir -p /data/pid && mkdir -p /data/logs && mkdir -p /data/data
RUN mkdir -p /data/collector
COPY bk-collector /data/collector/collector
RUN chmod +x /data/collector/collector
CMD /data/collector/collector -c /data/collector/config/collector.conf
