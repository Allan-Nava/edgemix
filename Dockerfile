# A static binary on scratch: edgemix reads files and makes no network calls at
# all, so the image needs neither CA certificates nor a resolver. There is no
# shell in it either — a container that can only run one program is one less
# thing to reason about when it is pointed at production logs.
FROM golang:1.27-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /edgemix ./cmd/edgemix

FROM scratch
COPY --from=build /edgemix /edgemix
# Mount the logs read-only, e.g.
#   docker run --rm -v /var/log:/logs:ro edgemix analyze /logs/haproxy.log
USER 65534:65534
ENTRYPOINT ["/edgemix"]
CMD ["help"]
