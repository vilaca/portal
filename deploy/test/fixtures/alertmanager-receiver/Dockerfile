# Receiver fixture image. Built and loaded into the e2e kind cluster by
# deploy/test/kind.sh; nothing in production depends on this.
FROM golang:1.22 AS builder
WORKDIR /src
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/receiver ./...

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/receiver /receiver
USER 65532:65532
EXPOSE 9093
ENTRYPOINT ["/receiver"]
