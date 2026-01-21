package database

// Migration represents a database migration
type Migration struct {
	Version string
	Up      string
	Down    string
}

// migrations contains all database migrations in order
var migrations = []Migration{
	{
		Version: "001_init",
		Up: `
-- Organizations table (for future multi-tenancy)
CREATE TABLE organizations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Users table
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    organization_id INTEGER NOT NULL DEFAULT 1,
    username TEXT UNIQUE NOT NULL,
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    full_name TEXT NOT NULL DEFAULT '',
    is_active BOOLEAN DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (organization_id) REFERENCES organizations(id)
);

CREATE INDEX idx_users_organization ON users(organization_id);
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_username ON users(username);

-- Roles table (management-level)
CREATE TABLE roles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    organization_id INTEGER NOT NULL DEFAULT 1,
    name TEXT NOT NULL,
    description TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (organization_id) REFERENCES organizations(id),
    UNIQUE(organization_id, name)
);

CREATE INDEX idx_roles_organization ON roles(organization_id);

-- Permissions table
CREATE TABLE permissions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    description TEXT,
    category TEXT
);

CREATE INDEX idx_permissions_category ON permissions(category);

-- Role-Permission mapping
CREATE TABLE role_permissions (
    role_id INTEGER NOT NULL,
    permission_id INTEGER NOT NULL,
    PRIMARY KEY (role_id, permission_id),
    FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE,
    FOREIGN KEY (permission_id) REFERENCES permissions(id) ON DELETE CASCADE
);

-- User-Role mapping (global roles)
CREATE TABLE user_roles (
    user_id INTEGER NOT NULL,
    role_id INTEGER NOT NULL,
    PRIMARY KEY (user_id, role_id),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE
);

-- Server-specific roles
CREATE TABLE server_roles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(server_id, name)
);

CREATE INDEX idx_server_roles_server ON server_roles(server_id);

-- Server Role-Permission mapping
CREATE TABLE server_role_permissions (
    server_role_id INTEGER NOT NULL,
    permission_id INTEGER NOT NULL,
    PRIMARY KEY (server_role_id, permission_id),
    FOREIGN KEY (server_role_id) REFERENCES server_roles(id) ON DELETE CASCADE,
    FOREIGN KEY (permission_id) REFERENCES permissions(id) ON DELETE CASCADE
);

-- User-ServerRole mapping
CREATE TABLE user_server_roles (
    user_id INTEGER NOT NULL,
    server_role_id INTEGER NOT NULL,
    PRIMARY KEY (user_id, server_role_id),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (server_role_id) REFERENCES server_roles(id) ON DELETE CASCADE
);

-- Refresh tokens table
CREATE TABLE refresh_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    token_hash TEXT UNIQUE NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    revoked BOOLEAN DEFAULT 0,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX idx_refresh_tokens_user ON refresh_tokens(user_id);
CREATE INDEX idx_refresh_tokens_hash ON refresh_tokens(token_hash);
CREATE INDEX idx_refresh_tokens_expires ON refresh_tokens(expires_at);

-- Audit logs table
CREATE TABLE audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER,
    action TEXT NOT NULL,
    resource_type TEXT,
    resource_id TEXT,
    ip_address TEXT,
    user_agent TEXT,
    success BOOLEAN DEFAULT 1,
    details TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE INDEX idx_audit_logs_user ON audit_logs(user_id);
CREATE INDEX idx_audit_logs_created ON audit_logs(created_at);
CREATE INDEX idx_audit_logs_resource ON audit_logs(resource_type, resource_id);

-- Insert default organization
INSERT INTO organizations (id, name) VALUES (1, 'Default Organization');

-- Insert default permissions
INSERT INTO permissions (name, description, category) VALUES
    -- System permissions
    ('manage_servers', 'Add/remove server definitions', 'system'),
    ('manage_users', 'Create users and assign roles', 'system'),
    ('view_audit_logs', 'View system audit logs', 'system'),
    ('manage_backups', 'Manage backup configurations', 'system'),
    ('manage_tasks', 'Create/edit scheduled tasks', 'system'),
    ('system_settings', 'Modify system configuration', 'system'),
    
    -- Server permissions
    ('server.view', 'View server status and details', 'server'),
    ('server.start', 'Start server', 'server'),
    ('server.stop', 'Stop server', 'server'),
    ('server.restart', 'Restart server', 'server'),
    ('server.console.view', 'View console output', 'server'),
    ('server.console.execute', 'Execute console commands', 'server'),
    ('server.config.view', 'View configuration files', 'server'),
    ('server.config.edit', 'Edit configuration files', 'server'),
    ('server.files.view', 'Browse server files', 'server'),
    ('server.files.edit', 'Modify server files', 'server'),
    ('server.backup.create', 'Create backups', 'server'),
    ('server.backup.restore', 'Restore from backups', 'server'),
    ('server.players.view', 'View player list', 'server'),
    ('server.players.kick', 'Kick players', 'server'),
    ('server.players.ban', 'Ban/unban players', 'server'),
    ('server.players.manage', 'Manage player roles/permissions', 'server');

-- Insert default roles
INSERT INTO roles (name, description) VALUES
    ('Admin', 'Full access to everything'),
    ('Operator', 'Manage servers and tasks, no user management'),
    ('user', 'Basic user with server management permissions'),
    ('Viewer', 'Read-only access');

-- Assign permissions to Admin role (all permissions)
INSERT INTO role_permissions (role_id, permission_id)
SELECT 1, id FROM permissions;

-- Assign permissions to Operator role (all except manage_users)
INSERT INTO role_permissions (role_id, permission_id)
SELECT 2, id FROM permissions WHERE name != 'manage_users';

-- Assign permissions to user role (manage servers and server operations)
INSERT INTO role_permissions (role_id, permission_id)
SELECT 3, id FROM permissions WHERE name IN ('manage_servers') OR name LIKE 'server.%';

-- Assign permissions to Viewer role (view-only permissions)
INSERT INTO role_permissions (role_id, permission_id)
SELECT 4, id FROM permissions WHERE name LIKE '%view%';
`,
		Down: `
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS user_server_roles;
DROP TABLE IF EXISTS server_role_permissions;
DROP TABLE IF EXISTS server_roles;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS permissions;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS organizations;
`,
	},
	{
		Version: "002_phase2a_tables",
		Up: `
-- Encrypted server credentials
CREATE TABLE server_credentials (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id TEXT UNIQUE NOT NULL,
    credential_type TEXT NOT NULL,      -- 'ssh_key', 'ssh_password'
    encrypted_value BLOB NOT NULL,      -- AES-256 encrypted
    encryption_key_id TEXT NOT NULL,    -- Version/ID of encryption key used
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_credentials_server ON server_credentials(server_id);

-- Time-series metrics (raw data - 2 day retention)
CREATE TABLE server_metrics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id TEXT NOT NULL,
    timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    cpu_usage REAL,                     -- Percentage (0-100)
    memory_used INTEGER,                -- Bytes
    memory_total INTEGER,               -- Bytes
    disk_used INTEGER,                  -- Bytes
    disk_total INTEGER,                 -- Bytes
    player_count INTEGER DEFAULT 0,
    network_rx INTEGER,                 -- Bytes received
    network_tx INTEGER,                 -- Bytes transmitted
    status TEXT                         -- 'online', 'offline', 'starting', etc.
);

CREATE INDEX idx_metrics_server_time ON server_metrics(server_id, timestamp DESC);

-- Aggregated hourly metrics (30 day retention)
CREATE TABLE server_metrics_hourly (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id TEXT NOT NULL,
    hour_timestamp DATETIME NOT NULL,   -- Rounded to hour
    avg_cpu_usage REAL,
    max_cpu_usage REAL,
    avg_memory_used INTEGER,
    max_memory_used INTEGER,
    avg_player_count REAL,
    max_player_count INTEGER,
    uptime_minutes INTEGER              -- Minutes server was online this hour
);

CREATE INDEX idx_metrics_hourly_server ON server_metrics_hourly(server_id, hour_timestamp DESC);

-- Daily metrics summary (1 year retention)
CREATE TABLE server_metrics_daily (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id TEXT NOT NULL,
    date DATE NOT NULL,
    avg_cpu_usage REAL,
    max_cpu_usage REAL,
    avg_memory_used INTEGER,
    max_memory_used INTEGER,
    avg_player_count REAL,
    max_player_count INTEGER,
    uptime_hours REAL,                  -- Hours server was online
    total_restarts INTEGER DEFAULT 0
);

CREATE INDEX idx_metrics_daily_server ON server_metrics_daily(server_id, date DESC);

-- Activity log (all server events)
CREATE TABLE activity_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    server_id TEXT,
    user_id INTEGER,
    activity_type TEXT NOT NULL,        -- 'server.start', 'command.execute', 'status.change', etc.
    description TEXT,
    metadata TEXT,                       -- JSON for additional context
    success BOOLEAN DEFAULT 1,
    error_message TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE INDEX idx_activity_server_time ON activity_log(server_id, timestamp DESC);
CREATE INDEX idx_activity_type_time ON activity_log(activity_type, timestamp DESC);
CREATE INDEX idx_activity_user ON activity_log(user_id, timestamp DESC);

-- Command history and queue
CREATE TABLE command_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id TEXT NOT NULL,
    user_id INTEGER,
    command TEXT NOT NULL,
    queued_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    executed_at DATETIME,
    completed_at DATETIME,
    status TEXT NOT NULL DEFAULT 'queued',  -- 'queued', 'executing', 'completed', 'failed', 'timeout'
    output TEXT,
    error TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE INDEX idx_command_server_time ON command_history(server_id, queued_at DESC);
CREATE INDEX idx_command_status ON command_history(status, queued_at);

-- Server status tracking
CREATE TABLE server_status (
    server_id TEXT PRIMARY KEY,
    status TEXT NOT NULL,               -- 'unknown', 'offline', 'starting', 'online', 'stopping', 'error'
    last_checked DATETIME,
    last_started DATETIME,
    last_stopped DATETIME,
    pid INTEGER,                        -- Process ID if known
    uptime_seconds INTEGER DEFAULT 0,
    error_message TEXT,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Connection pool tracking
CREATE TABLE ssh_connections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id TEXT NOT NULL,
    connected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_activity DATETIME DEFAULT CURRENT_TIMESTAMP,
    health_status TEXT DEFAULT 'healthy',  -- 'healthy', 'degraded', 'failed'
    reconnect_attempts INTEGER DEFAULT 0,
    is_active BOOLEAN DEFAULT 1
);

CREATE INDEX idx_connections_server ON ssh_connections(server_id, is_active);
`,
		Down: `
DROP TABLE IF EXISTS ssh_connections;
DROP TABLE IF EXISTS server_status;
DROP TABLE IF EXISTS command_history;
DROP TABLE IF EXISTS activity_log;
DROP TABLE IF EXISTS server_metrics_daily;
DROP TABLE IF EXISTS server_metrics_hourly;
DROP TABLE IF EXISTS server_metrics;
DROP TABLE IF EXISTS server_credentials;
`,
	},
	{
		Version: "003_backups",
		Up: `
-- Backups table
CREATE TABLE IF NOT EXISTS backups (
    id TEXT PRIMARY KEY,
    server_id TEXT NOT NULL,
    filename TEXT NOT NULL,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    destination_type TEXT NOT NULL,
    destination_path TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    error_message TEXT,
    metadata TEXT,
    created_by TEXT
);

CREATE INDEX IF NOT EXISTS idx_backups_server_id ON backups(server_id);
CREATE INDEX IF NOT EXISTS idx_backups_created_at ON backups(created_at);
CREATE INDEX IF NOT EXISTS idx_backups_status ON backups(status);

-- Backup schedules table
CREATE TABLE IF NOT EXISTS backup_schedules (
    id TEXT PRIMARY KEY,
    server_id TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    schedule TEXT NOT NULL,
    directories TEXT NOT NULL,
    destination_type TEXT NOT NULL,
    destination_path TEXT NOT NULL,
    retention_count INTEGER NOT NULL DEFAULT 7,
    last_run TIMESTAMP,
    next_run TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_backup_schedules_server_id ON backup_schedules(server_id);
CREATE INDEX IF NOT EXISTS idx_backup_schedules_next_run ON backup_schedules(next_run);
CREATE INDEX IF NOT EXISTS idx_backup_schedules_enabled ON backup_schedules(enabled);
`,
		Down: `
DROP TABLE IF EXISTS backup_schedules;
DROP TABLE IF EXISTS backups;
`,
	},
	{
		Version: "004_console",
		Up: `
-- Console command history
CREATE TABLE IF NOT EXISTS console_commands (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    command TEXT NOT NULL,
    executed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    exit_code INTEGER,
    output_preview TEXT,
    success BOOLEAN DEFAULT 1,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_console_commands_server ON console_commands(server_id, executed_at DESC);
CREATE INDEX IF NOT EXISTS idx_console_commands_user ON console_commands(user_id, executed_at DESC);
CREATE INDEX IF NOT EXISTS idx_console_commands_executed ON console_commands(executed_at DESC);

-- Active console sessions
CREATE TABLE IF NOT EXISTS console_sessions (
    id TEXT PRIMARY KEY,
    server_id TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    connected_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_activity TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    disconnected_at TIMESTAMP,
    is_active BOOLEAN DEFAULT 1,
    ip_address TEXT,
    user_agent TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_console_sessions_server ON console_sessions(server_id, is_active);
CREATE INDEX IF NOT EXISTS idx_console_sessions_user ON console_sessions(user_id, is_active);
CREATE INDEX IF NOT EXISTS idx_console_sessions_active ON console_sessions(is_active, connected_at DESC);

-- Console log files metadata
CREATE TABLE IF NOT EXISTS console_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id TEXT NOT NULL,
    log_path TEXT NOT NULL,
    size_bytes INTEGER DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    rotated_at TIMESTAMP,
    deleted_at TIMESTAMP,
    is_active BOOLEAN DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_console_logs_server ON console_logs(server_id, is_active);
CREATE INDEX IF NOT EXISTS idx_console_logs_created ON console_logs(created_at DESC);
`,
		Down: `
DROP TABLE IF EXISTS console_logs;
DROP TABLE IF EXISTS console_sessions;
DROP TABLE IF EXISTS console_commands;
`,
	},
	{
		Version: "005_releases",
		Up: `
CREATE TABLE IF NOT EXISTS releases (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    version TEXT NOT NULL,
    patchline TEXT NOT NULL DEFAULT 'default',
    file_path TEXT NOT NULL,
    file_size INTEGER NOT NULL DEFAULT 0,
    sha256 TEXT NOT NULL,
    downloader_version TEXT,
    downloaded_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    status TEXT NOT NULL DEFAULT 'ready'
);

CREATE INDEX IF NOT EXISTS idx_releases_version ON releases(version);
CREATE INDEX IF NOT EXISTS idx_releases_patchline ON releases(patchline);

CREATE TABLE IF NOT EXISTS release_jobs (
    id TEXT PRIMARY KEY,
    action TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME,
    finished_at DATETIME,
    output TEXT,
    error TEXT
);

INSERT OR IGNORE INTO permissions (name, description, category) VALUES
    ('manage_releases', 'Manage Hytale releases and downloader', 'system');

INSERT OR IGNORE INTO roles (name, description) VALUES
    ('ReleaseManager', 'Manage Hytale releases and downloader');

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name = 'manage_releases'
WHERE r.name = 'ReleaseManager';

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name = 'manage_releases'
WHERE r.name = 'Admin';
`,
		Down: `
DROP TABLE IF EXISTS release_jobs;
DROP TABLE IF EXISTS releases;
DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE name = 'manage_releases');
DELETE FROM roles WHERE name = 'ReleaseManager';
DELETE FROM permissions WHERE name = 'manage_releases';
`,
	},
	{
		Version: "006_release_permissions",
		Up: `
INSERT OR IGNORE INTO permissions (name, description, category) VALUES
    ('manage_releases', 'Manage Hytale releases and downloader', 'system');

INSERT OR IGNORE INTO roles (name, description) VALUES
    ('ReleaseManager', 'Manage Hytale releases and downloader');

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name = 'manage_releases'
WHERE r.name = 'ReleaseManager';

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name = 'manage_releases'
WHERE r.name = 'Admin';
`,
		Down: `
DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE name = 'manage_releases');
DELETE FROM roles WHERE name = 'ReleaseManager';
DELETE FROM permissions WHERE name = 'manage_releases';
`,
	},
	{
		Version: "007_release_sources",
		Up: `
ALTER TABLE releases ADD COLUMN source TEXT NOT NULL DEFAULT 'downloaded';
ALTER TABLE releases ADD COLUMN removed INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_releases_source ON releases(source);
CREATE INDEX IF NOT EXISTS idx_releases_removed ON releases(removed);
`,
		Down: `
DROP INDEX IF EXISTS idx_releases_removed;
DROP INDEX IF EXISTS idx_releases_source;
`,
	},
	{
		Version: "008_release_delete_permission",
		Up: `
INSERT OR IGNORE INTO permissions (name, description, category) VALUES
    ('releases.delete', 'Delete releases', 'releases');

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name = 'releases.delete'
WHERE r.name IN ('Admin', 'ReleaseManager');
`,
		Down: `
DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE name = 'releases.delete');
DELETE FROM permissions WHERE name = 'releases.delete';
`,
	},
	{
		Version: "009_server_dependencies_permission",
		Up: `
INSERT OR IGNORE INTO permissions (name, description, category) VALUES
    ('servers.dependencies.install', 'Install server dependencies', 'servers');

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name = 'servers.dependencies.install'
WHERE r.name IN ('Admin');
`,
		Down: `
DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE name = 'servers.dependencies.install');
DELETE FROM permissions WHERE name = 'servers.dependencies.install';
`,
	},
	{
		Version: "010_server_dependencies_check_permission",
		Up: `
INSERT OR IGNORE INTO permissions (name, description, category) VALUES
    ('servers.dependencies.check', 'Check server dependencies', 'servers');

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name = 'servers.dependencies.check'
WHERE r.name IN ('Admin');
`,
		Down: `
DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE name = 'servers.dependencies.check');
DELETE FROM permissions WHERE name = 'servers.dependencies.check';
`,
	},
	{
		Version: "011_server_release_deploy_permission",
		Up: `
INSERT OR IGNORE INTO permissions (name, description, category) VALUES
    ('servers.releases.deploy', 'Deploy release to server', 'servers');

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name = 'servers.releases.deploy'
WHERE r.name IN ('Admin');
`,
		Down: `
DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE name = 'servers.releases.deploy');
DELETE FROM permissions WHERE name = 'servers.releases.deploy';
`,
	},
	{
		Version: "012_server_transfer_benchmark_permission",
		Up: `
INSERT OR IGNORE INTO permissions (name, description, category) VALUES
    ('servers.transfer.benchmark', 'Benchmark server file transfer', 'servers');

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name = 'servers.transfer.benchmark'
WHERE r.name IN ('Admin');
`,
		Down: `
DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE name = 'servers.transfer.benchmark');
DELETE FROM permissions WHERE name = 'servers.transfer.benchmark';
`,
	},
	{
		Version: "007_release_manager_role_assignment",
		Up: `
INSERT OR IGNORE INTO user_roles (user_id, role_id)
SELECT ur.user_id, rm.id
FROM user_roles ur
JOIN roles admin_role ON admin_role.id = ur.role_id AND admin_role.name = 'Admin'
JOIN roles rm ON rm.name = 'ReleaseManager';
`,
		Down: `
DELETE FROM user_roles
WHERE role_id IN (SELECT id FROM roles WHERE name = 'ReleaseManager');
`,
	},
	{
		Version: "008_rbac_permissions_v2",
		Up: `
INSERT OR IGNORE INTO permissions (name, description, category) VALUES
    ('servers.list', 'List server definitions', 'servers'),
    ('servers.get', 'Get server definition', 'servers'),
    ('servers.create', 'Create server definition', 'servers'),
    ('servers.update', 'Update server definition', 'servers'),
    ('servers.delete', 'Delete server definition', 'servers'),
    ('servers.test_connection', 'Test server connectivity', 'servers'),
    ('servers.metrics.read', 'Read server metrics history', 'servers'),
    ('servers.metrics.latest', 'Read latest server metrics', 'servers'),
    ('servers.metrics.live', 'Read live server metrics', 'servers'),
    ('servers.activity.read', 'Read server activity log', 'servers'),
    ('servers.node_exporter.status', 'Read node_exporter status', 'servers'),
    ('servers.node_exporter.install', 'Install node_exporter', 'servers'),
    ('servers.start', 'Start server', 'servers'),
    ('servers.stop', 'Stop server', 'servers'),
    ('servers.restart', 'Restart server', 'servers'),
    ('servers.status.read', 'Read server runtime status', 'servers'),
    ('servers.console.view', 'View server console output', 'servers'),
    ('servers.console.execute', 'Execute server console commands', 'servers'),
    ('servers.console.history.read', 'Read console command history', 'servers'),
    ('servers.console.history.search', 'Search console command history', 'servers'),
    ('servers.console.autocomplete', 'Get console autocomplete', 'servers'),
    ('servers.tasks.read', 'Read server task status', 'servers'),
    ('servers.backups.create', 'Create backups', 'backups'),
    ('servers.backups.list', 'List backups', 'backups'),
    ('servers.backups.get', 'Get backup details', 'backups'),
    ('servers.backups.restore', 'Restore backups', 'backups'),
    ('servers.backups.delete', 'Delete backups', 'backups'),
    ('servers.backups.retention.enforce', 'Enforce backup retention', 'backups'),
    ('settings.get', 'Read system settings', 'settings'),
    ('settings.update', 'Update system settings', 'settings'),
    ('releases.list', 'List releases', 'releases'),
    ('releases.get', 'Get release details', 'releases'),
    ('releases.jobs.list', 'List release jobs', 'releases'),
    ('releases.jobs.get', 'Get release job details', 'releases'),
    ('releases.jobs.stream', 'Stream release job logs', 'releases'),
    ('releases.download', 'Download releases', 'releases'),
    ('releases.print_version', 'Print release version', 'releases'),
    ('releases.check_update', 'Check for updates', 'releases'),
    ('releases.downloader_version', 'Get downloader version', 'releases'),
    ('releases.reset_auth', 'Reset downloader auth', 'releases'),
    ('iam.users.list', 'List users', 'iam'),
    ('iam.users.get', 'Get user details', 'iam'),
    ('iam.users.create', 'Create users', 'iam'),
    ('iam.users.update', 'Update users', 'iam'),
    ('iam.users.delete', 'Delete users', 'iam'),
    ('iam.users.roles.update', 'Assign roles to users', 'iam'),
    ('iam.roles.list', 'List roles', 'iam'),
    ('iam.roles.get', 'Get role details', 'iam'),
    ('iam.roles.create', 'Create roles', 'iam'),
    ('iam.roles.update', 'Update roles', 'iam'),
    ('iam.roles.delete', 'Delete roles', 'iam'),
    ('iam.roles.permissions.update', 'Assign permissions to roles', 'iam'),
    ('iam.permissions.list', 'List permissions', 'iam'),
    ('iam.audit_logs.list', 'List audit logs', 'iam');

INSERT OR IGNORE INTO roles (name, description) VALUES
    ('Viewer', 'Read-only access'),
    ('Operator', 'Operate servers without IAM or settings changes'),
    ('Admin', 'Full administrative access'),
    ('IAMAdmin', 'Manage roles, permissions, and users'),
    ('ReleaseManager', 'Manage releases and downloader');

DELETE FROM role_permissions WHERE role_id IN (
    SELECT id FROM roles WHERE name IN ('Viewer', 'Operator', 'Admin', 'IAMAdmin', 'ReleaseManager')
);

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name IN (
    'servers.list',
    'servers.get',
    'servers.status.read',
    'servers.metrics.read',
    'servers.metrics.latest',
    'servers.metrics.live',
    'servers.activity.read',
    'servers.console.view',
    'servers.console.history.read',
    'servers.console.history.search',
    'servers.console.autocomplete',
    'servers.backups.list',
    'servers.backups.get',
    'settings.get'
)
WHERE r.name = 'Viewer';

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name IN (
    'servers.list',
    'servers.get',
    'servers.status.read',
    'servers.metrics.read',
    'servers.metrics.latest',
    'servers.metrics.live',
    'servers.activity.read',
    'servers.console.view',
    'servers.console.execute',
    'servers.console.history.read',
    'servers.console.history.search',
    'servers.console.autocomplete',
    'servers.tasks.read',
    'servers.start',
    'servers.stop',
    'servers.restart',
    'servers.test_connection',
    'servers.node_exporter.status',
    'servers.node_exporter.install',
    'servers.backups.create',
    'servers.backups.list',
    'servers.backups.get',
    'servers.backups.restore',
    'servers.backups.delete',
    'servers.backups.retention.enforce',
    'settings.get'
)
WHERE r.name = 'Operator';

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name IN (
    'servers.list',
    'servers.get',
    'servers.create',
    'servers.update',
    'servers.delete',
    'servers.test_connection',
    'servers.metrics.read',
    'servers.metrics.latest',
    'servers.metrics.live',
    'servers.activity.read',
    'servers.node_exporter.status',
    'servers.node_exporter.install',
    'servers.start',
    'servers.stop',
    'servers.restart',
    'servers.status.read',
    'servers.console.view',
    'servers.console.execute',
    'servers.console.history.read',
    'servers.console.history.search',
    'servers.console.autocomplete',
    'servers.tasks.read',
    'servers.backups.create',
    'servers.backups.list',
    'servers.backups.get',
    'servers.backups.restore',
    'servers.backups.delete',
    'servers.backups.retention.enforce',
    'settings.get',
    'settings.update',
    'releases.list',
    'releases.get',
    'releases.jobs.list',
    'releases.jobs.get',
    'releases.jobs.stream',
    'releases.download',
    'releases.print_version',
    'releases.check_update',
    'releases.downloader_version',
    'releases.reset_auth',
    'iam.users.list',
    'iam.users.get',
    'iam.users.create',
    'iam.users.update',
    'iam.users.delete',
    'iam.users.roles.update',
    'iam.roles.list',
    'iam.roles.get',
    'iam.roles.create',
    'iam.roles.update',
    'iam.roles.delete',
    'iam.roles.permissions.update',
    'iam.permissions.list',
    'iam.audit_logs.list'
)
WHERE r.name = 'Admin';

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name IN (
    'iam.users.list',
    'iam.users.get',
    'iam.users.create',
    'iam.users.update',
    'iam.users.delete',
    'iam.users.roles.update',
    'iam.roles.list',
    'iam.roles.get',
    'iam.roles.create',
    'iam.roles.update',
    'iam.roles.delete',
    'iam.roles.permissions.update',
    'iam.permissions.list',
    'iam.audit_logs.list'
)
WHERE r.name = 'IAMAdmin';

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name IN (
    'releases.list',
    'releases.get',
    'releases.jobs.list',
    'releases.jobs.get',
    'releases.jobs.stream',
    'releases.download',
    'releases.print_version',
    'releases.check_update',
    'releases.downloader_version',
    'releases.reset_auth'
)
WHERE r.name = 'ReleaseManager';
`,
		Down: `
DELETE FROM role_permissions WHERE permission_id IN (
    SELECT id FROM permissions WHERE name IN (
        'servers.list', 'servers.get', 'servers.create', 'servers.update', 'servers.delete',
        'servers.test_connection', 'servers.metrics.read', 'servers.metrics.latest', 'servers.metrics.live',
        'servers.activity.read', 'servers.node_exporter.status', 'servers.node_exporter.install',
        'servers.start', 'servers.stop', 'servers.restart',
        'servers.status.read', 'servers.console.view', 'servers.console.execute',
        'servers.console.history.read', 'servers.console.history.search', 'servers.console.autocomplete',
        'servers.tasks.read', 'servers.backups.create', 'servers.backups.list', 'servers.backups.get', 'servers.backups.restore',
        'servers.backups.delete', 'servers.backups.retention.enforce', 'settings.get', 'settings.update',
        'releases.list', 'releases.get', 'releases.jobs.list', 'releases.jobs.get', 'releases.jobs.stream',
        'releases.download', 'releases.print_version', 'releases.check_update', 'releases.downloader_version',
        'releases.reset_auth', 'iam.users.list', 'iam.users.get', 'iam.users.create', 'iam.users.update',
        'iam.users.delete', 'iam.users.roles.update', 'iam.roles.list', 'iam.roles.get', 'iam.roles.create',
        'iam.roles.update', 'iam.roles.delete', 'iam.roles.permissions.update', 'iam.permissions.list',
        'iam.audit_logs.list'
    )
);
DELETE FROM permissions WHERE name IN (
    'servers.list', 'servers.get', 'servers.create', 'servers.update', 'servers.delete',
    'servers.test_connection', 'servers.metrics.read', 'servers.metrics.latest', 'servers.metrics.live',
    'servers.activity.read', 'servers.node_exporter.status', 'servers.node_exporter.install',
    'servers.start', 'servers.stop', 'servers.restart',
    'servers.status.read', 'servers.console.view', 'servers.console.execute',
    'servers.console.history.read', 'servers.console.history.search', 'servers.console.autocomplete',
    'servers.tasks.read',
    'servers.backups.create', 'servers.backups.list', 'servers.backups.get', 'servers.backups.restore',
    'servers.backups.delete', 'servers.backups.retention.enforce', 'settings.get', 'settings.update',
    'releases.list', 'releases.get', 'releases.jobs.list', 'releases.jobs.get', 'releases.jobs.stream',
    'releases.download', 'releases.print_version', 'releases.check_update', 'releases.downloader_version',
    'releases.reset_auth', 'iam.users.list', 'iam.users.get', 'iam.users.create', 'iam.users.update',
    'iam.users.delete', 'iam.users.roles.update', 'iam.roles.list', 'iam.roles.get', 'iam.roles.create',
    'iam.roles.update', 'iam.roles.delete', 'iam.roles.permissions.update', 'iam.permissions.list',
    'iam.audit_logs.list'
);
`,
	},
	{
		Version: "009_remove_legacy_rbac",
		Up: `
DELETE FROM role_permissions
WHERE permission_id IN (
    SELECT id FROM permissions WHERE name IN (
        'manage_servers',
        'manage_users',
        'manage_releases',
        'view_audit_logs',
        'manage_backups',
        'manage_tasks',
        'system_settings',
        'server.start',
        'server.stop',
        'server.restart',
        'server.console.view',
        'server.console.execute'
    )
);

DELETE FROM permissions WHERE name IN (
    'manage_servers',
    'manage_users',
    'manage_releases',
    'view_audit_logs',
    'manage_backups',
    'manage_tasks',
    'system_settings',
    'server.start',
    'server.stop',
    'server.restart',
    'server.console.view',
    'server.console.execute'
);

DELETE FROM user_roles WHERE role_id IN (SELECT id FROM roles WHERE name = 'user');
DELETE FROM roles WHERE name = 'user';
`,
		Down: `
INSERT OR IGNORE INTO permissions (name, description, category) VALUES
    ('manage_servers', 'Add/remove server definitions', 'system'),
    ('manage_users', 'Create users and assign roles', 'system'),
    ('manage_releases', 'Manage releases', 'system'),
    ('view_audit_logs', 'View system audit logs', 'system'),
    ('manage_backups', 'Manage backup configurations', 'system'),
    ('manage_tasks', 'Create/edit scheduled tasks', 'system'),
    ('system_settings', 'Modify system configuration', 'system'),
    ('server.start', 'Start server', 'server'),
    ('server.stop', 'Stop server', 'server'),
    ('server.restart', 'Restart server', 'server'),
    ('server.console.view', 'View console output', 'server'),
    ('server.console.execute', 'Execute console commands', 'server');

INSERT OR IGNORE INTO roles (name, description) VALUES
    ('user', 'Legacy basic user role');
`,
	},
	{
		Version: "013_agent_install_permission",
		Up: `
INSERT OR IGNORE INTO permissions (name, description, category) VALUES
    ('servers.agent.install', 'Install monitoring agent', 'servers');

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name = 'servers.agent.install'
WHERE r.name IN ('Admin');
`,
		Down: `
DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE name = 'servers.agent.install');
DELETE FROM permissions WHERE name = 'servers.agent.install';
`,
	},
	{
		Version: "014_agent_certs",
		Up: `
CREATE TABLE agent_cert_requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    token TEXT UNIQUE NOT NULL,
    server_id TEXT NOT NULL,
    host_uuid TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL,
    used_at DATETIME,
    used_by_ip TEXT
);

CREATE INDEX idx_agent_cert_requests_token ON agent_cert_requests(token);
CREATE INDEX idx_agent_cert_requests_server ON agent_cert_requests(server_id);

CREATE TABLE agent_certificates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id TEXT NOT NULL,
    host_uuid TEXT NOT NULL,
    serial TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    cert_pem TEXT NOT NULL,
    issued_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    revoked_at DATETIME
);

CREATE INDEX idx_agent_certificates_server ON agent_certificates(server_id);
CREATE INDEX idx_agent_certificates_host ON agent_certificates(host_uuid);
CREATE INDEX idx_agent_certificates_serial ON agent_certificates(serial);
`,
		Down: `
DROP TABLE IF EXISTS agent_certificates;
DROP TABLE IF EXISTS agent_cert_requests;
`,
	},
	{
		Version: "015_agent_https_certs",
		Up: `
CREATE TABLE agent_https_certs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id TEXT NOT NULL,
    host_uuid TEXT NOT NULL,
    serial TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    cert_pem TEXT NOT NULL,
    key_pem TEXT NOT NULL,
    issued_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    revoked_at DATETIME
);

CREATE INDEX idx_agent_https_certs_server ON agent_https_certs(server_id);
CREATE INDEX idx_agent_https_certs_host ON agent_https_certs(host_uuid);
CREATE INDEX idx_agent_https_certs_serial ON agent_https_certs(serial);
`,
		Down: `
DROP TABLE IF EXISTS agent_https_certs;
`,
	},
	{
		Version: "016_agent_client_certs",
		Up: `
CREATE TABLE agent_client_certs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    serial TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    cert_pem TEXT NOT NULL,
    key_pem TEXT NOT NULL,
    issued_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    revoked_at DATETIME
);

CREATE INDEX idx_agent_client_certs_name ON agent_client_certs(name);
CREATE INDEX idx_agent_client_certs_serial ON agent_client_certs(serial);
`,
		Down: `
DROP TABLE IF EXISTS agent_client_certs;
`,
	},
	{
		Version: "017_agent_state_permission",
		Up: `
INSERT OR IGNORE INTO permissions (name, description, category) VALUES
    ('servers.agent.state.read', 'Read agent state', 'servers');

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name = 'servers.agent.state.read'
WHERE r.name IN ('Admin');
`,
		Down: `
DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE name = 'servers.agent.state.read');
DELETE FROM permissions WHERE name = 'servers.agent.state.read';
`,
	},
    {
        Version: "018_server_process_kill_permission",
        Up: `
INSERT OR IGNORE INTO permissions (name, description, category) VALUES
    ('servers.process.kill', 'Kill server process by PID', 'servers');

INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name = 'servers.process.kill'
WHERE r.name IN ('Admin');
`,
        Down: `
DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE name = 'servers.process.kill');
DELETE FROM permissions WHERE name = 'servers.process.kill';
`,
    },
    {
        Version: "019_backup_schedule_options",
        Up: `
ALTER TABLE backup_schedules ADD COLUMN exclude TEXT;
ALTER TABLE backup_schedules ADD COLUMN destination_config TEXT;
ALTER TABLE backup_schedules ADD COLUMN compression_type TEXT NOT NULL DEFAULT 'gzip';
ALTER TABLE backup_schedules ADD COLUMN compression_level INTEGER NOT NULL DEFAULT 6;

CREATE UNIQUE INDEX IF NOT EXISTS idx_backup_schedules_server_unique ON backup_schedules(server_id);
`,
        Down: `
DROP INDEX IF EXISTS idx_backup_schedules_server_unique;
`,
    },
    {
        Version: "020_backup_schedule_run_as_user",
        Up: `
ALTER TABLE backup_schedules ADD COLUMN run_as_user TEXT;
ALTER TABLE backup_schedules ADD COLUMN use_sudo BOOLEAN NOT NULL DEFAULT 0;
`,
        Down: `
`,
    },
    {
        Version: "021_backup_schedule_multi",
        Up: `
DROP INDEX IF EXISTS idx_backup_schedules_server_unique;
`,
        Down: `
`,
    },
}
