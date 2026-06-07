# CCC Wiki contact service. Two-stage: build a static, dependency-free binary,
# then ship it on distroless (CA certs for TLS + a nonroot user + minimal CVE
# surface). Base images are version-pinned, never :latest (enforced by make pins).
FROM golang:1.23-alpine AS build
WORKDIR /src
# Standard-library only, so go.mod is the whole module graph — no go.sum/vendor.
COPY go.mod ./
COPY *.go ./
COPY templates ./templates
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /contact .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /contact /contact
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/contact"]
