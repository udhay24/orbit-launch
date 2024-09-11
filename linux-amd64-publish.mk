# Define the name of the binary
BINARY_NAME = mediamtx

# Define base image for build
BASE_IMAGE = golang:1.22-alpine

# Define Docker repository
DOCKER_REPOSITORY = udhay24/yourrepository

# Dockerfile for building the binary
define DOCKERFILE_BUILD
FROM $(BASE_IMAGE) AS build-base
RUN apk add --no-cache zip make git tar
WORKDIR /s
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
ARG VERSION
ENV CGO_ENABLED 0
RUN rm -rf tmp binaries
RUN mkdir tmp binaries
RUN cp mediamtx.yml LICENSE tmp/
RUN go generate ./...

FROM build-base AS build-linux-amd64
RUN GOOS=linux GOARCH=amd64 go build -ldflags "-X github.com/bluenviron/mediamtx/internal/core.version=$$VERSION" -o tmp/$(BINARY_NAME)
RUN tar -C tmp -czf binaries/$(BINARY_NAME)_$${VERSION}_linux_amd64.tar.gz --owner=0 --group=0 $(BINARY_NAME) mediamtx.yml LICENSE

FROM $(BASE_IMAGE)
COPY --from=build-linux-amd64 /s/binaries /s/binaries
endef
export DOCKERFILE_BUILD

# Dockerfile for publishing to Docker Hub
define DOCKERFILE_DOCKERHUB
FROM scratch
ARG TARGETPLATFORM
ADD tmp/binaries/$$TARGETPLATFORM.tar.gz /
ENTRYPOINT [ "/mediamtx" ]
endef
export DOCKERFILE_DOCKERHUB

# Target to build and publish Docker image
dockerhub:
	$(eval VERSION := $(shell git describe --tags | tr -d v))

	docker login -u "udhay24" -p "mizkex-mavbag-Myrbo9"

	rm -rf tmp
	mkdir -p tmp tmp/binaries/linux/amd64

	# Build the binaries
	echo "$$DOCKERFILE_BUILD" | DOCKER_BUILDKIT=1 docker build . -f - \
	--build-arg VERSION=$$(git describe --tags) \
	-t temp-build

	# Extract the binaries
	docker run --rm -v $(PWD):/out \
	temp-build sh -c "cp -r /s/binaries/* /out/tmp/binaries/linux/amd64/"

	# Verify the binary was copied
	if [ ! -f tmp/binaries/linux/amd64/$(BINARY_NAME)_$$(git describe --tags)_linux_amd64.tar.gz ]; then \
		echo "Error: Binary not found"; \
		exit 1; \
	fi

	# Create and push the Docker image
	echo "$$DOCKERFILE_DOCKERHUB" | docker buildx build . -f - \
	--provenance=false \
	--platform=linux/amd64 \
	-t $(DOCKER_REPOSITORY):$(VERSION) \
	-t $(DOCKER_REPOSITORY):latest \
	--push

	docker buildx rm builder
	rm -rf $$HOME/.docker/manifests/*

.PHONY: dockerhub
