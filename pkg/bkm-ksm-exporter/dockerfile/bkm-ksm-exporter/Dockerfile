FROM centos:7

RUN curl -o /etc/yum.repos.d/CentOS-Base.repo https://mirrors.tencent.com/repo/centos7_base.repo || true

RUN mkdir -p /data/bkm-ksm-exporter
COPY bkm-ksm-exporter /data/bkm-ksm-exporter/bkm-ksm-exporter
RUN chmod +x /data/bkm-ksm-exporter/bkm-ksm-exporter

# Run as a non-root user: the exporter only binds :8080 and reads HPAs through
# its ServiceAccount, so it needs no root privileges.
RUN groupadd -r bkm && useradd -r -g bkm -u 1000 bkm
USER 1000

ENTRYPOINT ["/data/bkm-ksm-exporter/bkm-ksm-exporter"]
