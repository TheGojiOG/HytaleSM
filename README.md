# Hytale Server Manager

A web-based management platform for Hytale game servers with RBAC, backup management, and real-time console capabilities.

## Release Overview

### Core Functionality
- Server inventory and status visibility.
- Remote lifecycle actions (start, stop, restart).
- Backup creation and retention workflows.
- Release management with downloader support.
- RBAC-based access control and audit logging.

### Supported Systems
- Windows 10/11 (development and local deployment).
- Linux (systemd service supported).
- macOS (development use; service install not included).

## Prerequisites
- Go (backend go run)
- Node.js and npm (frontend npm run dev)
- Python 3 (backend control helper)

## Quick Start

### 1) Configure runtime files
- Copy example configs into active files:
  - configs/config.example.yaml -> configs/config.yaml
  - configs/servers.example.yaml -> configs/servers.yaml
  - configs/tasks.example.yaml -> configs/tasks.yaml (optional)

### 2) Start services

#### Windows
- Start: scripts/start-server.ps1
- Stop: scripts/stop-server.ps1

#### Linux/macOS
- Start: scripts/start-server.sh
- Stop: scripts/stop-server.sh

#### Linux systemd service (persists across reboot)
- Copy scripts/systemd/hytale-server-manager.service to /etc/systemd/system/
- Update WorkingDirectory, ExecStart, ExecStop, User, and Group to match your install path and user
- The service uses scripts/start-server.sh --service (no prebuilt artifacts required)
- Enable and start the service with systemd

### 3) First-time admin setup
- After startup, open http://localhost:5173/setup to create the initial admin user.

## Security and Local Secrets
- Startup scripts generate JWT_SECRET and ENCRYPTION_KEY once and store them in .env.
- The .env file is loaded on each start to keep a single local configuration.

## Packaging Notes
- This repository does not ship prebuilt binaries or frontend build artifacts.
- Use the start scripts to run in development mode.

## Support
Use the GitHub issue tracker for bugs and feature requests.
