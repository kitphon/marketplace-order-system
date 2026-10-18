#!/usr/bin/env bash

set -Eeuo pipefail

mode="${1:-}"
case "${mode}" in
  integration|smoke|all) ;;
  *)
    echo "usage: $0 {integration|smoke|all}" >&2
    exit 2
    ;;
esac

for command in go docker; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "required command not found: ${command}" >&2
    exit 1
  fi
done
if ! docker compose version >/dev/null 2>&1; then
  echo "Docker Compose v2 is required (docker compose)" >&2
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  echo "Docker daemon is not available" >&2
  exit 1
fi

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
run_id="$(date +%s)-$$"
compose_project="mos-m5-test-${run_id}"
order_database_name="${M5_TEST_ORDER_MONGODB_DATABASE:-${M5_TEST_MONGODB_DATABASE:-order_m5_test_${run_id//-/_}}}"
inventory_database_name="${M5_TEST_INVENTORY_MONGODB_DATABASE:-inventory_m5_test_${run_id//-/_}}"
if [[ "${order_database_name}" == "${inventory_database_name}" ]]; then
  echo "Order and Inventory test database names must be different" >&2
  exit 2
fi
run_directory="$(mktemp -d "${TMPDIR:-/tmp}/mos-m5-test.XXXXXX")"
binary_directory="${run_directory}/bin"
log_directory="${run_directory}/logs"
mkdir -p "${binary_directory}" "${log_directory}"

product_address="${M5_TEST_PRODUCT_ADDR:-127.0.0.1:55051}"
inventory_address="${M5_TEST_INVENTORY_ADDR:-127.0.0.1:55052}"
order_address="${M5_TEST_ORDER_ADDR:-127.0.0.1:58080}"
mongodb_port="${M5_TEST_MONGODB_PORT:-57017}"
overall_timeout_seconds="${M5_TEST_TIMEOUT_SECONDS:-180}"
compose_file="${repository_root}/deploy/docker-compose.test.yml"

export M5_TEST_MONGODB_PORT="${mongodb_port}"
export M5_TEST_PRODUCT_ADDR="${product_address}"
export M5_TEST_INVENTORY_ADDR="${inventory_address}"
export M5_TEST_ORDER_URL="http://${order_address}"
export M5_TEST_MONGODB_URI="mongodb://127.0.0.1:${mongodb_port}/?replicaSet=rs0&directConnection=true"
export M5_TEST_ORDER_MONGODB_DATABASE="${order_database_name}"
export M5_TEST_INVENTORY_MONGODB_DATABASE="${inventory_database_name}"
export M5_TEST_MONGODB_DATABASE="${order_database_name}"
export M5_TEST_RUN_ID="${run_id}"

product_pid=""
inventory_pid=""
order_pid=""
watchdog_pid=""
compose_started=0

print_logs() {
  echo "--- Product log ---" >&2
  if [[ -f "${log_directory}/product.log" ]]; then
    sed -n '1,240p' "${log_directory}/product.log" >&2
  fi
  echo "--- Inventory log ---" >&2
  if [[ -f "${log_directory}/inventory.log" ]]; then
    sed -n '1,240p' "${log_directory}/inventory.log" >&2
  fi
  echo "--- Order log ---" >&2
  if [[ -f "${log_directory}/order.log" ]]; then
    sed -n '1,240p' "${log_directory}/order.log" >&2
  fi
  if [[ "${compose_started}" == "1" ]]; then
    echo "--- MongoDB logs ---" >&2
    docker compose -p "${compose_project}" -f "${compose_file}" logs --no-color --tail=160 >&2 || true
  fi
}

stop_child() {
  local pid="$1"
  if [[ -z "${pid}" ]] || ! kill -0 "${pid}" 2>/dev/null; then
    return
  fi
  kill -TERM "${pid}" 2>/dev/null || true
  local attempt
  for ((attempt = 0; attempt < 100; attempt++)); do
    if ! kill -0 "${pid}" 2>/dev/null; then
      wait "${pid}" 2>/dev/null || true
      return
    fi
    sleep 0.1
  done
  kill -KILL "${pid}" 2>/dev/null || true
  wait "${pid}" 2>/dev/null || true
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if [[ -n "${watchdog_pid}" ]]; then
    kill "${watchdog_pid}" 2>/dev/null || true
    wait "${watchdog_pid}" 2>/dev/null || true
  fi
  if [[ "${status}" -ne 0 ]]; then
    print_logs
  fi
  stop_child "${order_pid}"
  stop_child "${inventory_pid}"
  stop_child "${product_pid}"
  if [[ "${compose_started}" == "1" ]]; then
    docker compose -p "${compose_project}" -f "${compose_file}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "${run_directory}"
  exit "${status}"
}

trap cleanup EXIT
trap 'exit 130' INT TERM

if [[ ! "${overall_timeout_seconds}" =~ ^[1-9][0-9]*$ ]]; then
  echo "M5_TEST_TIMEOUT_SECONDS must be a positive integer" >&2
  exit 2
fi
runner_pid=$$
(
  sleep "${overall_timeout_seconds}"
  echo "test stack exceeded ${overall_timeout_seconds}s overall timeout" >&2
  kill -TERM "${runner_pid}" 2>/dev/null || true
) &
watchdog_pid=$!

address_port() {
  local address="$1"
  local port="${address##*:}"
  if [[ ! "${port}" =~ ^[1-9][0-9]*$ ]] || ((port > 65535)); then
    echo "invalid test address: ${address}" >&2
    exit 2
  fi
  printf '%s' "${port}"
}

assert_port_available() {
  local label="$1"
  local port="$2"
  if (echo >/dev/tcp/127.0.0.1/"${port}") >/dev/null 2>&1; then
    echo "${label} test port ${port} is already in use; override its M5_TEST_* setting" >&2
    exit 1
  fi
}

assert_port_available "Product" "$(address_port "${product_address}")"
assert_port_available "Inventory" "$(address_port "${inventory_address}")"
assert_port_available "Order" "$(address_port "${order_address}")"
address_port "127.0.0.1:${mongodb_port}" >/dev/null
assert_port_available "MongoDB" "${mongodb_port}"

echo "Building service binaries..."
go -C "${repository_root}/services/product" build -o "${binary_directory}/product" ./cmd/grpc
go -C "${repository_root}/services/inventory" build -o "${binary_directory}/inventory" ./cmd/grpc
go -C "${repository_root}/services/order" build -o "${binary_directory}/order" ./cmd/api
export M5_TEST_INVENTORY_BINARY="${binary_directory}/inventory"

echo "Starting isolated MongoDB replica set (${compose_project})..."
compose_started=1
docker compose -p "${compose_project}" -f "${compose_file}" up -d --wait --wait-timeout 60 mongodb
docker compose -p "${compose_project}" -f "${compose_file}" run --rm mongodb-init

PRODUCT_GRPC_ADDR="${product_address}" \
  "${binary_directory}/product" >"${log_directory}/product.log" 2>&1 &
product_pid=$!

start_inventory() {
  INVENTORY_GRPC_ADDR="${inventory_address}" \
  MONGODB_URI="${M5_TEST_MONGODB_URI}" \
  MONGODB_DATABASE="${inventory_database_name}" \
    "${binary_directory}/inventory" >>"${log_directory}/inventory.log" 2>&1 &
  inventory_pid=$!
}

: >"${log_directory}/inventory.log"
start_inventory

HTTP_ADDR="${order_address}" \
PRODUCT_GRPC_ADDR="${product_address}" \
INVENTORY_GRPC_ADDR="${inventory_address}" \
MONGODB_URI="${M5_TEST_MONGODB_URI}" \
MONGODB_DATABASE="${order_database_name}" \
  "${binary_directory}/order" >"${log_directory}/order.log" 2>&1 &
order_pid=$!

run_tests() {
  local test_pattern="$1"
  go -C "${repository_root}/services/order" test \
    -tags=integration \
    ./integration \
    -run "${test_pattern}" \
    -count=1 \
    -timeout=90s \
    -v
}

echo "Running ${mode} black-box tests..."
case "${mode}" in
  smoke)
    run_tests '^TestSmoke$'
    ;;
  integration|all)
    run_tests '^Test(Integration|InventoryRuntimePersistenceBeforeRestart|InventoryStartupFailsWithoutMongoDB)$'
    echo "Restarting Inventory service to verify durable state..."
    stop_child "${inventory_pid}"
    inventory_pid=""
    start_inventory
    if [[ "${mode}" == "all" ]]; then
      run_tests '^Test(InventoryRuntimePersistenceAfterRestart|Smoke)$'
    else
      run_tests '^TestInventoryRuntimePersistenceAfterRestart$'
    fi
    ;;
esac

echo "${mode} tests passed"
