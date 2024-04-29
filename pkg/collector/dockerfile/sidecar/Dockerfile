FROM centos:7

VOLUME /data
RUN yum clean all && rm -f /var/lib/rpm/__db* && rpm --rebuilddb
RUN yum install -y iproute strace tcpdump

RUN mkdir -p /data/sidecar
COPY sidecar /data/sidecar/sidecar
RUN chmod +x /data/sidecar/sidecar
CMD /data/sidecar/sidecar -config /data/sidecar/config/sidecar.yaml
