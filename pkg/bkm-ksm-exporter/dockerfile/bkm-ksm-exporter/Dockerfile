FROM centos:7

RUN curl -o /etc/yum.repos.d/CentOS-Base.repo https://mirrors.tencent.com/repo/centos7_base.repo || true

RUN mkdir -p /data/bkm-ksm-exporter
COPY bkm-ksm-exporter /data/bkm-ksm-exporter/bkm-ksm-exporter
RUN chmod +x /data/bkm-ksm-exporter/bkm-ksm-exporter

ENTRYPOINT ["/data/bkm-ksm-exporter/bkm-ksm-exporter"]
