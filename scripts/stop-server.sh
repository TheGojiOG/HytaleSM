#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
pids_file="$root_dir/.dev-pids"

if [[ ! -f "$pids_file" ]]; then
  echo "No .dev-pids file found. Are services running?"
  exit 0
fi

mapfile -t pids < "$pids_file"
for pid in "${pids[@]}"; do
  if [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1; then
    echo "Stopping PID $pid"
    kill "$pid" || true
  fi
done

rm -f "$pids_file"
echo "Stopped dev services."
