#!/usr/bin/env bash
set -euo pipefail

# Robust smoke test: dockerized broker + local producer/consumer
# Usage:
#   ./scripts/smoke_with_docker.sh               # defaults: 50 msgs, go-pub-sub:local, port 50051
#   ./scripts/smoke_with_docker.sh -n 100 -i my/image:tag -p 50052
#
# Options:
#   -n NUM_MSGS    number of messages to publish (default 50)
#   -i IMAGE_NAME  docker image name (default go-pub-sub:local)
#   -p HOST_PORT   host port to map to container 50051 (default 50051)

# defaults
NUM_MSGS=50
IMAGE_NAME="go-pub-sub:local"
HOST_PORT=50051

# parse flags
while getopts "n:i:p:h" opt; do
  case ${opt} in
    n) NUM_MSGS="${OPTARG}" ;;
    i) IMAGE_NAME="${OPTARG}" ;;
    p) HOST_PORT="${OPTARG}" ;;
    h)
      echo "Usage: $0 [-n NUM_MSGS] [-i IMAGE_NAME] [-p HOST_PORT]"
      exit 0
      ;;
    \?)
      echo "Invalid option: -${OPTARG}" >&2
      exit 1
      ;;
  esac
done
shift $((OPTIND -1))

# validate NUM_MSGS
if ! [[ "${NUM_MSGS}" =~ ^[0-9]+$ ]] || [ "${NUM_MSGS}" -le 0 ]; then
  echo "ERROR: NUM_MSGS must be a positive integer. Got: ${NUM_MSGS}"
  exit 1
fi

CONTAINER_NAME="go-pub-broker-smoke"
BROKER_ADDR="localhost:${HOST_PORT}"
TOPIC="events"
GROUP="g1"

# Paths (you mentioned client/consumer & client/producer)
CONSUMER_CMD="./client/consumer"
PRODUCER_CMD="./client/producer"

# Logs
BROKER_LOG="/tmp/gopub_broker.log"
CONSUMER_LOG="/tmp/gopub_consumer.log"
FOLLOW_LOG="/tmp/gopub_follow.log"

# Timeouts (seconds)
READY_TIMEOUT=30
SUBSCRIBE_TIMEOUT=15
RECEIVE_TIMEOUT=60

# arrays and pids
PROD_PIDS=()
CONSUMER_PID=""
LOG_FOLLOW_PID=""

cleanup() {
  echo "[smoke] cleanup..."
  # kill consumer if running
  if [[ -n "${CONSUMER_PID:-}" ]]; then
    if ps -p "${CONSUMER_PID}" >/dev/null 2>&1; then
      kill "${CONSUMER_PID}" 2>/dev/null || true
    fi
  fi

  # kill producers
  if [[ ${#PROD_PIDS[@]} -gt 0 ]]; then
    for pid in "${PROD_PIDS[@]}"; do
      if [[ -n "${pid}" ]] && ps -p "${pid}" >/dev/null 2>&1; then
        kill "${pid}" 2>/dev/null || true
      fi
    done
  fi

  # kill log follower
  if [[ -n "${LOG_FOLLOW_PID:-}" ]]; then
    if ps -p "${LOG_FOLLOW_PID}" >/dev/null 2>&1; then
      kill "${LOG_FOLLOW_PID}" 2>/dev/null || true
    fi
  fi

  # remove docker container if exists
  if docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}\$"; then
    docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
  fi

  # remove follow log
  rm -f "${FOLLOW_LOG}" || true
}
trap cleanup EXIT

# -----------------------
# Helpers
# -----------------------
port_is_free() {
  local port=$1
  if command -v ss >/dev/null 2>&1; then
    ! ss -ltn "( sport = :${port} )" 2>/dev/null | grep -q LISTEN
  elif command -v lsof >/dev/null 2>&1; then
    ! lsof -i :"${port}" >/dev/null 2>&1
  elif command -v netstat >/dev/null 2>&1; then
    ! netstat -ltn 2>/dev/null | grep -q ":${port} "
  else
    # can't detect, assume free
    return 0
  fi
}

docker_image_exists() {
  local img=$1
  docker image inspect "${img}" >/dev/null 2>&1
}

# -----------------------
# Prereqs
# -----------------------
for cmd in docker go; do
  if ! command -v ${cmd} >/dev/null 2>&1; then
    echo "ERROR: '${cmd}' is required but not installed."
    exit 2
  fi
done

echo "[smoke] starting smoke test: image=${IMAGE_NAME} host_port=${HOST_PORT} messages=${NUM_MSGS}"

# Remove old logs
rm -f "${BROKER_LOG}" "${CONSUMER_LOG}" "${FOLLOW_LOG}"

# Check image exists
if ! docker_image_exists "${IMAGE_NAME}"; then
  echo "[smoke] ERROR: Docker image '${IMAGE_NAME}' not found. Build it first (e.g., make build-image)."
  exit 1
fi

# Check host port is free
if ! port_is_free "${HOST_PORT}"; then
  echo "[smoke] ERROR: host port ${HOST_PORT} appears to be in use. Please free it or specify a different HOST_PORT."
  if command -v ss >/dev/null 2>&1; then ss -ltn | grep ":${HOST_PORT}" || true; fi
  if command -v lsof >/dev/null 2>&1; then lsof -i :"${HOST_PORT}" || true; fi
  if command -v netstat >/dev/null 2>&1; then netstat -ltn | grep ":${HOST_PORT}" || true; fi
  exit 1
fi

# -----------------------
# Start container
# -----------------------
echo "[smoke] starting broker container from image ${IMAGE_NAME} (host port ${HOST_PORT})..."
RUN_OUTPUT=""
if ! RUN_OUTPUT=$(docker run -d --rm --name "${CONTAINER_NAME}" -p "${HOST_PORT}":50051 \
    -v "$(pwd)/config/config.yaml:/etc/gopubsub/config.yaml:ro" \
    "${IMAGE_NAME}" 2>&1); then
  echo "[smoke] ERROR: docker run failed."
  echo "---- docker run output ----"
  echo "${RUN_OUTPUT}"
  exit 1
fi

CONTAINER_ID="${RUN_OUTPUT}"
sleep 0.3

# verify container running
if ! docker ps --filter "id=${CONTAINER_ID}" --format '{{.Status}} {{.Names}}' | grep -q "Up"; then
  echo "[smoke] ERROR: container did not reach running state."
  echo "---- docker ps (matching container) ----"
  docker ps -a --filter "name=${CONTAINER_NAME}" --format "table {{.ID}}\t{{.Names}}\t{{.Status}}\t{{.Ports}}"
  echo "---- docker logs (latest) ----"
  docker logs "${CONTAINER_ID}" || true
  exit 1
fi

echo "[smoke] container started: id=${CONTAINER_ID}"
echo "[smoke] docker port mapping:"
docker port "${CONTAINER_ID}" 50051/tcp || true

# -----------------------
# Readiness: follow docker logs live and wait for the readiness line
# -----------------------
READY_KEYWORD="[server] gRPC broker listening on"
echo -n "[smoke] waiting for broker log signal ('${READY_KEYWORD}') (max ${READY_TIMEOUT}s)"

# Start a background log follower writing to FOLLOW_LOG
docker logs -f "${CONTAINER_ID}" > "${FOLLOW_LOG}" 2>&1 &
LOG_FOLLOW_PID=$!

ready_elapsed=0
while true; do
  if grep -F -q "${READY_KEYWORD}" "${FOLLOW_LOG}" 2>/dev/null; then
    echo " ✅ detected broker readiness"
    break
  fi

  sleep 1
  ready_elapsed=$((ready_elapsed + 1))
  echo -n "."

  if [ "${ready_elapsed}" -ge "${READY_TIMEOUT}" ]; then
    echo
    echo "[smoke] ❌ broker didn't log readiness within ${READY_TIMEOUT}s."
    echo "---- docker logs ----"
    docker logs "${CONTAINER_ID}" || true
    # kill follower
    if [[ -n "${LOG_FOLLOW_PID:-}" ]]; then
      kill "${LOG_FOLLOW_PID}" 2>/dev/null || true
    fi
    exit 1
  fi
done

# stop follower cleanly
if [[ -n "${LOG_FOLLOW_PID:-}" ]]; then
  kill "${LOG_FOLLOW_PID}" 2>/dev/null || true
fi
sleep 0.1
echo "[smoke] broker ready"

# -----------------------
# Start consumer locally and capture log
# -----------------------
echo "[smoke] starting consumer (local go run)..."
if [[ ! -x "${CONSUMER_CMD}" && ! -d "${CONSUMER_CMD}" && ! -f "${CONSUMER_CMD}.go" ]]; then
  echo "[smoke] ERROR: consumer command not found at ${CONSUMER_CMD}"
  exit 1
fi

# run consumer (no --offset)
go run "${CONSUMER_CMD}" --addr="${BROKER_ADDR}" --topic="${TOPIC}" --group="${GROUP}" > "${CONSUMER_LOG}" 2>&1 &
CONSUMER_PID=$!
echo "[smoke] consumer pid=${CONSUMER_PID}"

# Wait for consumer subscription
echo -n "[smoke] waiting for consumer subscription (max ${SUBSCRIBE_TIMEOUT}s)"
sub_elapsed=0
SUB_OK=0
while true; do
  if grep -E -q "subscribed|Subscribed to|subscribed to topic|🧩 subscribed|subscribed to" "${CONSUMER_LOG}" 2>/dev/null; then
    echo " ready"
    SUB_OK=1
    break
  fi
  sleep 1
  sub_elapsed=$((sub_elapsed + 1))
  echo -n "."
  if [ "${sub_elapsed}" -ge "${SUBSCRIBE_TIMEOUT}" ]; then
    echo
    echo "[smoke] consumer did not subscribe within ${SUBSCRIBE_TIMEOUT}s. Consumer log:"
    tail -n 200 "${CONSUMER_LOG}" || true
    echo "---- docker logs ----"
    docker logs "${CONTAINER_ID}" || true
    exit 1
  fi
done

# -----------------------
# Publish messages via local producers
# -----------------------
echo "[smoke] publishing ${NUM_MSGS} messages via local producer..."
PROD_PIDS=()
for i in $(seq 1 "${NUM_MSGS}"); do
  if [[ ! -x "${PRODUCER_CMD}" && ! -d "${PRODUCER_CMD}" && ! -f "${PRODUCER_CMD}.go" ]]; then
    echo "[smoke] ERROR: producer command not found at ${PRODUCER_CMD}"
    exit 1
  fi
  go run "${PRODUCER_CMD}" --addr="${BROKER_ADDR}" --topic="${TOPIC}" --msg="smoke-msg-${i}" > /dev/null 2>&1 &
  PROD_PIDS+=("$!")
done

# Wait for producers to finish
for p in "${PROD_PIDS[@]}"; do
  if ! wait "${p}"; then
    echo "[smoke] note: producer process ${p} exited with non-zero status (ignored)"
  fi
done

# Poll consumer log until we see NUM_MSGS or timeout
echo "[smoke] waiting up to ${RECEIVE_TIMEOUT}s for ${NUM_MSGS} messages to be received by consumer..."
receive_elapsed=0
RECEIVED=0
while true; do
  RECEIVED=$(grep -E -c "received|➡ received|msg received" "${CONSUMER_LOG}" || true)
  if [ "${RECEIVED}" -ge "${NUM_MSGS}" ]; then
    echo "[smoke] ✅ received ${RECEIVED}/${NUM_MSGS}"
    break
  fi
  sleep 1
  receive_elapsed=$((receive_elapsed + 1))
  if [ "${receive_elapsed}" -ge "${RECEIVE_TIMEOUT}" ]; then
    echo "[smoke] ❌ timeout waiting for messages. received=${RECEIVED}/${NUM_MSGS}"
    echo "---- docker logs (tail) ----"
    docker logs "${CONTAINER_ID}" | tail -n 200 || true
    echo "---- consumer logs (tail) ----"
    tail -n 200 "${CONSUMER_LOG}" || true
    exit 1
  fi
done

# Success
ACKED=$(grep -E -c "acked|✅ acked|ack" "${CONSUMER_LOG}" || true)
echo "[smoke] summary: received=${RECEIVED}, acked=${ACKED}"

exit 0
