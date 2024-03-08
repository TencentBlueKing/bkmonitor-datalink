FROM centos:7

VOLUME /data
RUN yum clean all && rm -f /var/lib/rpm/__db* && rpm --rebuilddb
RUN yum install -y iproute strace tcpdump

RUN mkdir -p /data/sliwebhook/logs
COPY sliwebhook /data/sliwebhook/sliwebhook
RUN chmod +x /data/sliwebhook/sliwebhook
CMD /data/sliwebhook/sliwebhook -c /data/sliwebhook/sliwebhook.yaml
