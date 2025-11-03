#!/usr/bin/env bash
set -euo pipefail

# smoke_with_docker.sh
# Run broker in Docker (must have built image as 'go-pub-sub:local'),
# run consumer & producer locally (go run).
# Usage: ./scripts/smoke_test.sh [NUM_MSGS]
# Example: ./scripts/smoke_test.sh 50

NUM_MSGS=${1:-50}
IMAGE_NAME="go-pub-sub:local"
CONTAINER_NAME="go-pub-broker"
BROKER_ADDR="localhost:50051"
TOPIC="events"
GROUP="g1"

BROKER_LOG="/tmp/gopub_broker.log"
CONSUMER_LOG="/tmp/gopub_consumer.log"

BROKER_PID=""
CONSUMER_PID=""

# Cleanup handler
cleanup() {
  echo "[smoke] cleanup..."
  if [[ -n "${CONSUMER_PID-}" ]]; then
    kill "${CONSUMER_PID}" 2>/dev/null || true
  fi
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# Ensure docker present
if ! command -v docker >/dev/null 2>&1; then
  echo "ERROR: docker is required. Install docker and try again."
  exit 2
fi

# Ensure go present (for installing grpcurl and running clients)
if ! command -v go >/dev/null 2>&1; then
  echo "ERROR: go is required (for grpcurl install and running clients). Install go and try again."
  exit 2
fi

# Ensure grpcurl present or install it via 'go install'
if ! command -v grpcurl >/dev/null 2>&1; then
  echo "[smoke] grpcurl not found — attempting to install with 'go install'..."
  if go env GOPATH >/dev/null 2>&1; then
    # install into GOPATH/bin or GOBIN
    GO_BIN=$(go env GOBIN 2>/dev/null || true)
    if [[ -z "${GO_BIN}" ]]; then
      GO_BIN="$(go env GOPATH)/bin"
    fi
    echo "[smoke] installing grpcurl to ${GO_BIN} ..."
    # Use go install for latest module
    GO111MODULE=on go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
    # ensure ~/.local/bin or GOPATH/bin is on PATH in interactive shells; the script will try to use the binary from GOBIN/GOPATH/bin
    if [[ ! -x "${GO_BIN}/grpcurl" ]]; then
      echo "WARNING: grpcurl installed to ${GO_BIN}/grpcurl but it is not on PATH for this shell."
      echo "You can add it to PATH or run: export PATH=\$PATH:$(go env GOPATH)/bin"
      # still continue and try to call via full path
      GRPCURL_CMD="${GO_BIN}/grpcurl"
    else
      GRPCURL_CMD="grpcurl"
    fi
  else
    echo "ERROR: unable to determine GOPATH; please install grpcurl manually."
    exit 2
  fi
else
  GRPCURL_CMD="grpcurl"
fi

# Remove old logs
rm -f "${BROKER_LOG}" "${CONSUMER_LOG}"

# Start container (server)
echo "[smoke] starting broker container from image ${IMAGE_NAME}..."
docker run -d --rm --name "${CONTAINER_NAME}" -p 50051:50051 \
  -v "$(pwd)/config/config.yaml:/etc/gopubsub/config.yaml:ro" \
  "${IMAGE_NAME}" > /dev/null

# wait for container to be listed
sleep 1

# Wait until grpc server is ready
echo -n "[smoke] waiting for broker to be ready"
for i in $(seq 1 60); do
  if "${GRPCURL_CMD}" -plaintext ${BROKER_ADDR} list >/dev/null 2>&1; then
    echo " ready"
    break
  fi
  echo -n "."
  sleep 0.5
  if [ $i -eq 60 ]; then
    echo
    echo "[smoke] broker didn't become ready in time. Dumping docker logs for diagnosis:"
    docker logs "${CONTAINER_NAME}" || true
    exit 1
  fi
done

# Start consumer locally (go run) and write to consumer log
echo "[smoke] starting consumer (local go run)..."
go run ./cmd/consumer --addr=${BROKER_ADDR} --topic=${TOPIC} --group=${GROUP} --offset=0 > "${CONSUMER_LOG}" 2>&1 &
CONSUMER_PID=$!
echo "[smoke] consumer pid=${CONSUMER_PID}"

# Wait for consumer to report subscription
echo -n "[smoke] waiting for consumer subscription"
SUB_OK=0
for i in $(seq 1 20); do
  if grep -E -q "subscribed|Subscribed to|subscribed to topic" "${CONSUMER_LOG}" 2>/dev/null; then
    echo " ready"
    SUB_OK=1
    break
  fi
  echo -n "."
  sleep 0.5
done

if [ "${SUB_OK}" -ne 1 ]; then
  echo
  echo "[smoke] consumer did not show subscription. Dumping consumer log:"
  tail -n +1 "${CONSUMER_LOG}" || true
  exit 1
fi

# Publish messages using local producer executables (go run)
echo "[smoke] publishing ${NUM_MSGS} messages via local producer..."
PROD_PIDS=()
for i in $(seq 1 ${NUM_MSGS}); do
  go run ./cmd/producer --addr=${BROKER_ADDR} --topic=${TOPIC} --msg="smoke-msg-${i}" > /dev/null 2>&1 &
  PROD_PIDS+=($!)
done

# wait for producers to exit
for pid in "${PROD_PIDS[@]}"; do
  wait "${pid}" || true
done

echo "[smoke] waiting for consumer to process messages..."
sleep 2

RECEIVED=$(grep -E -c "received|➡ received" "${CONSUMER_LOG}" || true)
ACKED=$(grep -E -c "acked|✅ acked|ack" "${CONSUMER_LOG}" || true)

echo "[smoke] received=${RECEIVED}, acked=${ACKED}"

# On success, print small summary and exit 0
if [ "${RECEIVED}" -ge "${NUM_MSGS}" ]; then
  echo "[smoke] ✅ SUCCESS: received ${RECEIVED}/${NUM_MSGS}"
  exit 0
else
  echo "[smoke] ❌ FAILURE: only received ${RECEIVED}/${NUM_MSGS}"
  echo "---- docker broker logs (tail) ----"
  docker logs "${CONTAINER_NAME}" | tail -n 200 || true
  echo "---- consumer logs (tail) ----"
  tail -n 200 "${CONSUMER_LOG}" || true
  exit 1
fi
