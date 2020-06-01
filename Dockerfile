FROM docker.io/golang:1.14-stretch AS builder

WORKDIR /root/src
COPY *.go go.mod go.sum /root/src/
RUN go build -o bnw-thumb

FROM docker.io/busybox:1-glibc

COPY --from=builder /root/src/bnw-thumb /bnw-thumb

ENTRYPOINT ["/bnw-thumb"]
