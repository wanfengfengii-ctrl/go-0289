#!/usr/bin/env bash
# Smoke test for the environmental DNA contamination verdict service.
# Builds the server, starts it against a temporary data directory, drives a
# real lock-to-projection flow over HTTP, then cleans up every process and
# temporary file. Runs fully offline and deterministically.
set -euo pipefail

PORT="${SMOKE_PORT:-18089}"
ADDR="127.0.0.1:${PORT}"
BASE="http://${ADDR}"
WORKDIR_TMP="$(mktemp -d)"
DATA_DIR="${WORKDIR_TMP}/data"
BIN="${WORKDIR_TMP}/server"
SERVER_PID=""

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  rm -rf "${WORKDIR_TMP}"
}
trap cleanup EXIT

echo "==> building server"
go build -o "${BIN}" ./cmd/server

mkdir -p "${DATA_DIR}"
echo "==> starting server on ${ADDR}"
ADDR="${ADDR}" DATA_DIR="${DATA_DIR}" "${BIN}" &
SERVER_PID=$!

# Wait for the health endpoint to become ready.
health=""
for _ in $(seq 1 50); do
  if health="$(curl -sS "${BASE}/api/v1/health" 2>/dev/null || true)"; then
    if [[ -n "${health}" ]]; then
      break
    fi
  fi
  sleep 0.1
done

if [[ -z "${health}" ]]; then
  echo "server did not become healthy" >&2
  exit 1
fi
echo "health: ${health}"
if [[ "${health}" != *'"status":"ok"'* ]]; then
  echo "unexpected health payload" >&2
  exit 1
fi

# Create a protocol and lock a batch.
proto='{
  "id":"smoke-proto",
  "target":"smoke-target",
  "scale":1000,
  "threshold":10,
  "baseline_start":0,
  "baseline_end":4,
  "window":3,
  "positive_min":6000,
  "positive_max":8000,
  "replicate_count":2,
  "reagent_lot":"smoke-lot",
  "layout":{
    "plate_id":"P","rows":8,"cols":12,
    "samples":[
      {"replicate_group":"S1","tubes":[
        {"tube_code":"T1","well":{"plate":"P","row":1,"col":1}},
        {"tube_code":"T2","well":{"plate":"P","row":1,"col":2}}
      ]}
    ],
    "controls":[
      {"kind":"positive","well":{"plate":"P","row":8,"col":1}},
      {"kind":"negative","well":{"plate":"P","row":8,"col":2}}
    ]
  }
}'

create_resp="$(curl -sS -X POST "${BASE}/api/v1/protocols" -H 'Content-Type: application/json' -d "${proto}")"
echo "create protocol: ${create_resp}"
if [[ "${create_resp}" != *'"snapshot"'* ]]; then
  echo "protocol creation failed" >&2
  exit 1
fi

lock_resp="$(curl -sS -X POST "${BASE}/api/v1/batches/smoke-batch/lock" -H 'Content-Type: application/json' -d '{"protocol_id":"smoke-proto"}')"
echo "lock batch: ${lock_resp}"
if [[ "${lock_resp}" != *'"digest"'* ]]; then
  echo "batch lock failed" >&2
  exit 1
fi

batches_resp="$(curl -sS "${BASE}/api/v1/batches")"
echo "list batches: ${batches_resp}"
if [[ "${batches_resp}" != *'smoke-batch'* ]]; then
  echo "locked batch missing from projection" >&2
  exit 1
fi

# Restart the server against the same data directory to verify recovery.
kill "${SERVER_PID}" 2>/dev/null || true
wait "${SERVER_PID}" 2>/dev/null || true
SERVER_PID=""

ADDR="${ADDR}" DATA_DIR="${DATA_DIR}" "${BIN}" &
SERVER_PID=$!

recovered=""
for _ in $(seq 1 50); do
  if recovered="$(curl -sS "${BASE}/api/v1/batches" 2>/dev/null || true)"; then
    if [[ -n "${recovered}" ]]; then
      break
    fi
  fi
  sleep 0.1
done

if [[ "${recovered}" != *'smoke-batch'* ]]; then
  echo "batch not recovered after restart" >&2
  exit 1
fi
echo "recovered batches: ${recovered}"

echo "==> smoke test passed"
