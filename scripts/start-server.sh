#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
backend_dir="$root_dir/backend"
frontend_dir="$root_dir/frontend"
log_dir="$root_dir/logs"
env_file="$root_dir/.env"
mode="dev"

if [[ "${1:-}" == "--service" ]]; then
  mode="service"
  shift
fi

mkdir -p "$log_dir"

pick_python() {
  if command -v python3 >/dev/null 2>&1; then
    echo "python3"
    return
  fi
  if command -v python >/dev/null 2>&1; then
    echo "python"
    return
  fi
  echo ""
}

PYTHON_BIN="$(pick_python)"
if [[ -z "$PYTHON_BIN" ]]; then
  echo "Python is required (python3 or python)." >&2
  exit 1
fi

gen_secret() {
  "$PYTHON_BIN" - <<'PY'
import base64, os
print(base64.b64encode(os.urandom(32)).decode())
PY
}

ensure_env_secret() {
  local name="$1"
  local value=""

  if [[ -n "${!name:-}" ]]; then
    value="${!name}"
  elif [[ -f "$env_file" ]]; then
    value="$(grep -E "^${name}=" "$env_file" | head -n1 | cut -d= -f2- || true)"
  fi

  if [[ -z "$value" ]]; then
    value="$(gen_secret)"
    echo "${name}=${value}" >> "$env_file"
  fi

  export "$name"="$value"
}

touch "$env_file"
ensure_env_secret JWT_SECRET
ensure_env_secret ENCRYPTION_KEY

if [[ "$mode" == "service" ]]; then
  echo "Starting backend (go run) in service mode..."
  (
    cd "$backend_dir"
    "$PYTHON_BIN" server_control.py start --foreground --go
  ) &
  backend_pid=$!

  echo "Starting frontend (npm run dev) in service mode..."
  (
    cd "$frontend_dir"
    npm run dev
  ) &
  frontend_pid=$!

  printf "%s\n%s\n" "$backend_pid" "$frontend_pid" > "$root_dir/.dev-pids"

  cleanup() {
    if kill -0 "$backend_pid" >/dev/null 2>&1; then
      kill "$backend_pid" || true
    fi
    if kill -0 "$frontend_pid" >/dev/null 2>&1; then
      kill "$frontend_pid" || true
    fi
    wait "$backend_pid" 2>/dev/null || true
    wait "$frontend_pid" 2>/dev/null || true
  }

  trap cleanup SIGINT SIGTERM

  wait -n "$backend_pid" "$frontend_pid"
  cleanup
  exit $?
fi

echo "Starting backend (go run)..."
(
  cd "$backend_dir"
  "$PYTHON_BIN" server_control.py start --foreground --go
) >"$log_dir/dev-backend.log" 2>&1 &
backend_pid=$!

echo "Starting frontend (npm run dev)..."
(
  cd "$frontend_dir"
  npm run dev
) >"$log_dir/dev-frontend.log" 2>&1 &
frontend_pid=$!

printf "%s\n%s\n" "$backend_pid" "$frontend_pid" > "$root_dir/.dev-pids"

echo "Backend PID: $backend_pid"
echo "Frontend PID: $frontend_pid"
echo "Logs: $log_dir/dev-backend.log, $log_dir/dev-frontend.log"
echo "Run scripts/stop-server.sh to stop services."
