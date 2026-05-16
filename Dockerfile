# Portal production image.
#
# Two stages: golang builder → distroless static nonroot. The final image
# carries only the portal binary (plus libc-free static dependencies). Built
# from the repo root; release.yml passes --build-arg VERSION=$tag so the
# binary's --version is wired to the git tag.

ARG GO_VERSION=1.22
ARG DISTROLESS=gcr.io/distroless/static:nonroot

FROM golang:${GO_VERSION} AS builder
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

# Dependency layer first so iterative builds re-use the cache.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0 \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH:-amd64}

RUN go build \
        -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/portal \
        ./cmd/portal

# ----------------------------------------------------------------------
FROM ${DISTROLESS}
COPY --from=builder /out/portal /portal
USER 65532:65532
ENTRYPOINT ["/portal"]
CMD ["run"]
