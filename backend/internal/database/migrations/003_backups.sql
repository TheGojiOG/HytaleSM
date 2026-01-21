-- Migration 003: Backups table
-- Stores backup metadata and tracking information

CREATE TABLE IF NOT EXISTS backups (
    id TEXT PRIMARY KEY,
    server_id TEXT NOT NULL,
    filename TEXT NOT NULL,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    destination_type TEXT NOT NULL, -- 'local', 'sftp', 's3'
    destination_path TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending', -- 'pending', 'creating', 'completed', 'failed', 'deleted'
    error_message TEXT,
    metadata TEXT, -- JSON metadata: directories backed up, compression type, etc.
    created_by TEXT,
    FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_backups_server_id ON backups(server_id);
CREATE INDEX IF NOT EXISTS idx_backups_created_at ON backups(created_at);
CREATE INDEX IF NOT EXISTS idx_backups_status ON backups(status);

-- Backup schedules table
CREATE TABLE IF NOT EXISTS backup_schedules (
    id TEXT PRIMARY KEY,
    server_id TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    schedule TEXT NOT NULL, -- Cron expression
    directories TEXT NOT NULL, -- JSON array
    exclude TEXT, -- JSON array
    destination_type TEXT NOT NULL,
    destination_path TEXT NOT NULL,
    destination_config TEXT, -- JSON destination config
    retention_count INTEGER NOT NULL DEFAULT 7,
    compression_type TEXT NOT NULL DEFAULT 'gzip',
    compression_level INTEGER NOT NULL DEFAULT 6,
    run_as_user TEXT,
    use_sudo BOOLEAN NOT NULL DEFAULT 0,
    last_run TIMESTAMP,
    next_run TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_backup_schedules_server_id ON backup_schedules(server_id);
CREATE INDEX IF NOT EXISTS idx_backup_schedules_next_run ON backup_schedules(next_run);
CREATE INDEX IF NOT EXISTS idx_backup_schedules_enabled ON backup_schedules(enabled);
