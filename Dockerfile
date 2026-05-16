FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/proxy-hub ./cmd/server
RUN mkdir -p /out/data

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/proxy-hub /proxy-hub
COPY --from=build --chown=65532:65532 /out/data /data
ENV DATA_DIR=/data
VOLUME ["/data"]
USER 65532:65532
ENTRYPOINT ["/proxy-hub"]
