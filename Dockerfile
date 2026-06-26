ARG GO_VERSION=1.26
ARG BUILDPLATFORM=linux/amd64

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" \
    -o /out/z-courier-gateway ./cmd/gateway

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S zcourier \
    && adduser -S -G zcourier zcourier

WORKDIR /app

COPY --from=build /out/z-courier-gateway /usr/local/bin/z-courier-gateway
COPY configs ./configs
COPY conf ./conf

RUN mkdir -p /app/log \
    && chown -R zcourier:zcourier /app/log /app/configs /app/conf

USER zcourier

ENV ZCOURIER_CONFIG=/app/configs/z-courier.yaml
ENV ZINX_CONFIG_FILE_PATH=/app/conf/zinx.json

EXPOSE 8999 18080

ENTRYPOINT ["z-courier-gateway"]
CMD ["-config", "/app/configs/z-courier.yaml"]
