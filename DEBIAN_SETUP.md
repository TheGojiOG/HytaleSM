# Debian Setup Guide (New Server)

This guide walks through installing prerequisites, configuring the project, and running it as a persistent systemd service.

## 1) System Requirements
- Debian 12 (recommended) or Debian 11
- Root or sudo access
- Internet access for package installs and npm dependencies

## 2) Install Prerequisites

### System packages
Run:
- sudo apt-get update
- sudo apt-get install -y git curl ca-certificates python3 build-essential

### Go
Install Go from the official tarball. Check https://go.dev/dl/ for the latest version and set it below.
Run:
- GO_VERSION=1.25.6
- curl -LO https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz
- sudo rm -rf /usr/local/go
- sudo tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz
- echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh
- . /etc/profile.d/go.sh
- go version

### Node.js
Install Node.js LTS from NodeSource (Node 20 LTS).
Run:
- curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
- sudo apt-get install -y nodejs
- node -v
- npm -v

## 3) Create a Service User
Create a dedicated user and group for running the service.
Run:
- sudo useradd -m -d /opt/hytale-server-manager -s /bin/bash hytale
- sudo passwd -l hytale
- sudo mkdir -p /opt/hytale-server-manager
- sudo chown -R hytale:hytale /opt/hytale-server-manager

## 4) Clone and Install Dependencies
Clone the repo into /opt/hytale-server-manager and install frontend dependencies.
Run:
- sudo -u hytale git clone https://github.com/TheGojiOG/HytaleSM.git /opt/hytale-server-manager
- sudo -u hytale bash -lc "cd /opt/hytale-server-manager/frontend && npm install"

## 5) Configure Runtime Files
Copy example configs into active files.
Run:
- sudo -u hytale cp /opt/hytale-server-manager/configs/config.example.yaml /opt/hytale-server-manager/configs/config.yaml
- sudo -u hytale cp /opt/hytale-server-manager/configs/servers.example.yaml /opt/hytale-server-manager/configs/servers.yaml
- sudo -u hytale cp /opt/hytale-server-manager/configs/tasks.example.yaml /opt/hytale-server-manager/configs/tasks.yaml

Review configs/config.yaml to confirm:
- HTTP bind address and port
- CORS allowlist
- Database path

## 6) Start with systemd
Use the provided systemd unit template.
Run:
- sudo cp /opt/hytale-server-manager/scripts/systemd/hytale-server-manager.service /etc/systemd/system/hytale-server-manager.service
- sudo nano /etc/systemd/system/hytale-server-manager.service

Update the service file to match your environment:
- WorkingDirectory=/opt/hytale-server-manager
- ExecStart=/bin/bash /opt/hytale-server-manager/scripts/start-server.sh --service
- ExecStop=/bin/bash /opt/hytale-server-manager/scripts/stop-server.sh
- User=hytale
- Group=hytale

Enable and start:
- sudo systemctl daemon-reload
- sudo systemctl enable hytale-server-manager
- sudo systemctl start hytale-server-manager

Check status:
- sudo systemctl status hytale-server-manager

## 7) First-Time Admin Setup
Open the UI in a browser and create the first admin user:
- http://<server-ip>:5173/setup

## 8) Firewall Notes
If using UFW, allow the frontend and backend ports.
Run:
- sudo apt-get install -y ufw
- sudo ufw allow 5173/tcp
- sudo ufw allow 8080/tcp
- sudo ufw enable
- sudo ufw status

## 9) Logs and Troubleshooting
Run:
- sudo journalctl -u hytale-server-manager -f

Notes:
- Backend output: server_control.py runs in foreground under the service.
- Frontend output: npm run dev output is in the systemd journal.

## 10) Updating
Run:
- sudo -u hytale git -C /opt/hytale-server-manager pull
- sudo -u hytale bash -lc "cd /opt/hytale-server-manager/frontend && npm install"
- sudo systemctl restart hytale-server-manager
