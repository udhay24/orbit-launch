FROM golang:1.22-alpine AS build-base
RUN apk add --no-cache zip make git tar
WORKDIR /s
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
ARG VERSION
ENV CGO_ENABLED 0
ENV BINARY_NAME mediamtx
RUN rm -rf tmp binaries
RUN mkdir tmp binaries
RUN cp mediamtx.yml LICENSE tmp/
RUN go generate ./...

FROM build-base AS build-linux-amd64
RUN GOOS=linux GOARCH=amd64 go build -ldflags "-X github.com/bluenviron/mediamtx/internal/core.version=$$VERSION" -o tmp/$(BINARY_NAME)
RUN tar -C tmp -czf binaries/$(BINARY_NAME)_$${VERSION}_linux_amd64.tar.gz --owner=0 --group=0 $(BINARY_NAME) mediamtx.yml LICENSE

FROM golang:1.22-alpine
COPY --from=build-linux-amd64 /s/binaries /s/binaries
