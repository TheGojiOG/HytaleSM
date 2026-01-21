set -e
export DEBIAN_FRONTEND=noninteractive
if command -v node_exporter >/dev/null 2>&1; then
  echo 'node_exporter already installed'
  exit 0
fi
SUDO=''
if [ $(id -u) -ne 0 ]; then SUDO='sudo'; fi
if command -v apt-get >/dev/null 2>&1; then
  $SUDO apt-get update -y
  $SUDO apt-get install -y prometheus-node-exporter
elif command -v dnf >/dev/null 2>&1; then
  $SUDO dnf install -y node_exporter || $SUDO dnf install -y prometheus-node-exporter
elif command -v yum >/dev/null 2>&1; then
  $SUDO yum install -y node_exporter || $SUDO yum install -y prometheus-node-exporter
elif command -v pacman >/dev/null 2>&1; then
  $SUDO pacman -Sy --noconfirm prometheus-node-exporter
else
  echo 'Unsupported package manager'
  exit 2
fi
if command -v systemctl >/dev/null 2>&1; then
  UNIT=''
  if systemctl list-unit-files --type=service | grep -q '^prometheus-node-exporter.service'; then UNIT='prometheus-node-exporter.service'; fi
  if [ -z "$UNIT" ] && systemctl list-unit-files --type=service | grep -q '^node_exporter.service'; then UNIT='node_exporter.service'; fi
  if [ -n "$UNIT" ]; then $SUDO systemctl enable --now "$UNIT" || true; fi
fi
if command -v service >/dev/null 2>&1; then
  if [ -x /etc/init.d/prometheus-node-exporter ]; then $SUDO service prometheus-node-exporter start 2>/dev/null; fi
  if [ -x /etc/init.d/node_exporter ]; then $SUDO service node_exporter start 2>/dev/null; fi
fi
if command -v update-rc.d >/dev/null 2>&1; then
  $SUDO update-rc.d prometheus-node-exporter defaults 2>/dev/null || true
  $SUDO update-rc.d node_exporter defaults 2>/dev/null || true
fi
