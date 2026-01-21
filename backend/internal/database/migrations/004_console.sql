-- Migration 004: Console and Real-time Features
-- Stores console command history and active session tracking

-- Console command history
-- Tracks all commands executed via the console with user attribution
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
-- Tracks WebSocket connections to console streams
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
-- Tracks console log files for rotation and cleanup
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
