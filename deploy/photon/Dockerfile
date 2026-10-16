# syntax=docker/dockerfile:1.7
#
# Self-hosted Photon geocoder (optional; compose profiles "geocoder" and
# "geocoder-import"). Photon 1.x needs Java 21+. The index lives in the
# photon-data volume and is built by the "import" command from the
# GraphHopper Central Asia dump, filtered to Kazakhstan.

FROM eclipse-temurin:21-jre-alpine
ARG PHOTON_VERSION=1.3.0

RUN apk add --no-cache zstd \
    && addgroup -S photon \
    && adduser -S -G photon -h /photon photon \
    && mkdir -p /opt/photon /photon \
    && chown photon:photon /photon

ADD https://github.com/komoot/photon/releases/download/${PHOTON_VERSION}/photon-${PHOTON_VERSION}.jar /opt/photon/photon.jar
COPY entrypoint.sh /opt/photon/entrypoint.sh
RUN chmod 0644 /opt/photon/photon.jar && chmod 0755 /opt/photon/entrypoint.sh

USER photon
WORKDIR /photon
EXPOSE 2322
ENTRYPOINT ["/opt/photon/entrypoint.sh"]
CMD ["serve"]
