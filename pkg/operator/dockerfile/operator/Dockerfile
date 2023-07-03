FROM centos:7

VOLUME /data
RUN yum clean all && rm -f /var/lib/rpm/__db* && rpm --rebuilddb
RUN yum install -y iproute strace tcpdump

RUN mkdir -p /data/bkmonitor-operator/config
RUN mkdir -p /data/bkmonitor-operator/logs
COPY bkmonitor-operator /data/bkmonitor-operator/bkmonitor-operator
RUN chmod +x /data/bkmonitor-operator/bkmonitor-operator
CMD /data/bkmonitor-operator/bkmonitor-operator -c /data/bkmonitor-operator/config/bkmonitor-operator.yaml run
