FROM centos:7

VOLUME /data
RUN yum clean all && rm -f /var/lib/rpm/__db* && rpm --rebuilddb
RUN yum install -y iproute strace tcpdump

RUN mkdir -p /data/reloader/config
RUN mkdir -p /data/bkmonitorbeat/config/child_configs && mkdir -p /data/pid
COPY bkmonitorbeat-reloader /data/reloader/reloader
RUN chmod +x /data/reloader/reloader
CMD /data/reloader/reloader -c /data/reloader/config/reloader.yaml reloader
