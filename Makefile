# Makefile for go-pub-sub: build image, run, smoke-test, push
# Usage examples:
#   make build-image
#   make run-image
#   make smoke-test NUM_MSGS=50
#   make stop-image

# Configurable variables
IMAGE_NAME ?= go-pub-sub
TAG ?= latest

CONFIG_PATH ?= config/config.yaml
CONTAINER_NAME ?= go-pub-broker
BROKER_PORT ?= 50051

.PHONY: all build-image run-image stop-image logs smoke-test push clean

all: build-image

# Build local image (uses Dockerfile at repo root)
build-image:
	docker build -t $(IMAGE_NAME):local .

# Tag for dockerhub (local tag remains IMAGE_NAME:local)
tag-image:
	docker tag $(IMAGE_NAME):local $(IMAGE)

# Run container in background, mapping port and mounting config (override config if desired)
run-image:
	@echo "[make] running image (name=$(CONTAINER_NAME))..."
	docker run -d --rm --name $(CONTAINER_NAME) \
	  -p $(BROKER_PORT):50051 \
	  -v "$(PWD)/config/$(notdir $(CONFIG_PATH)):/etc/gopubsub/config.yaml:ro" \
	  $(IMAGE_NAME):local

# Show container logs (follow)
logs:
	docker logs -f $(CONTAINER_NAME)

# Stop the running container
stop-image:
	-docker rm -f $(CONTAINER_NAME) 2>/dev/null || true
	-@echo "[make] stopped container $(CONTAINER_NAME)"

# Run the smoke test.
smoke-test:
	@NUM_MSGS=${NUM_MSGS:-50} IMAGE_NAME=${IMAGE_NAME:-go-pub-sub:local} ./scripts/smoke_with_docker.sh ${NUM_MSGS} ${IMAGE_NAME}


clean:
	-docker rmi $(IMAGE_NAME):local $(IMAGE) || true
	-@echo "[make] cleaned images"
