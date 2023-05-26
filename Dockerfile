FROM golang:1.20.4-alpine3.18 as dev

ENV GO111MODULE=on
ENV GOPROXY=https://goproxy.cn,direct

COPY . /src/
WORKDIR /src

RUN set -x \
    && apk add git ca-certificates make \
    && make build

# ---------- 8< ----------

FROM alpine:3.18

ENV GIN_MODE=release
ENV HELM_CACHE_HOME=/data/cache
ENV HELM_CONFIG_HOME=/data/config
ENV HELM_DATA_HOME=/data/share
ENV HELM_PLUGINS=/data/plugins

ENV TZ=Asia/Shanghai
RUN set -x \
    && ln -snf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo 'Asia/Shanghai' > /etc/timezone

RUN sed -i 's@https://dl-cdn.alpinelinux.org@https://mirrors.ustc.edu.cn@g' /etc/apk/repositories \
    && apk add --no-cache bash tzdata ca-certificates

COPY config-example.yaml  /config.yaml
COPY --from=dev /src/bin/helm-wrapper /helm-wrapper

ENTRYPOINT [ "/helm-wrapper" ]
