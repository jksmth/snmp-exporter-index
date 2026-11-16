# Drop-in snmp_exporter. Same flags, port, config path, and Prometheus text.
# A scrape URL may also pass index=<instance suffix>.

ARG SNMP_EXPORTER_VERSION=v0.31.0
ARG GOLANG_VERSION=1.26.0

FROM golang:${GOLANG_VERSION}-bookworm AS build
ARG SNMP_EXPORTER_VERSION
ARG TARGETARCH=amd64
ARG BUILD_DATE=unknown
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git python3 \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
RUN git clone --depth 1 --branch "${SNMP_EXPORTER_VERSION}" https://github.com/prometheus/snmp_exporter.git .
COPY exporter /opt/overlay/exporter
COPY scripts/apply.py /opt/overlay/scripts/apply.py
RUN python3 /opt/overlay/scripts/apply.py /src \
    && rm -rf .git
RUN CGO_ENABLED=0 GOOS=linux GOARCH="${TARGETARCH}" go build -trimpath \
    -ldflags="-s -w -X github.com/prometheus/common/version.Version=${SNMP_EXPORTER_VERSION}-index -X github.com/prometheus/common/version.Revision=index -X github.com/prometheus/common/version.BuildUser=snmp-exporter-index -X github.com/prometheus/common/version.BuildDate=${BUILD_DATE}" \
    -o /out/snmp_exporter .

ARG TARGETARCH=amd64
FROM quay.io/prometheus/busybox-linux-${TARGETARCH}:latest
LABEL org.opencontainers.image.title="snmp_exporter"
LABEL org.opencontainers.image.description="snmp_exporter with an optional index query parameter"
LABEL org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /src/LICENSE /LICENSE
COPY --from=build /src/NOTICE /NOTICE
COPY --from=build /src/snmp.yml /etc/snmp_exporter/snmp.yml
COPY examples/if_index.yml /etc/snmp_exporter/if_index.yml
COPY --from=build /out/snmp_exporter /bin/snmp_exporter
EXPOSE 9116
ENTRYPOINT ["/bin/snmp_exporter"]
CMD ["--config.file=/etc/snmp_exporter/snmp.yml"]
