// API Response Types

export interface User {
  id: number;
  organization_id?: number;
  username: string;
  email: string;
  full_name?: string;
  is_active: boolean;
  created_at: string;
  updated_at: string;
}

export interface RoleSummary {
  id: number;
  name: string;
}

export interface UserWithRoles extends User {
  roles: RoleSummary[];
}

export interface Permission {
  id: number;
  name: string;
  description: string;
  category: string;
}

export interface Role {
  id: number;
  name: string;
  description: string;
  permissions: string[];
}

export interface AuthResponse {
  access_token: string;
  refresh_token: string;
  expires_at: string;
  user: User;
}

export interface Server {
  id: string;
  name: string;
  description?: string;
  host?: string;
  port?: number;
  connection_status?: 'disconnected' | 'online' | 'running';
  ssh_port?: number;
  ssh_user?: string;
  working_directory?: string;
  start_command?: string;
  stop_command?: string;
  connection?: {
    host: string;
    port: number;
    username: string;
    auth_method: 'key' | 'password';
    key_path?: string;
    key_content?: string;
    password?: string;
  };
  server?: {
    working_directory: string;
    executable: string;
    java_args?: string;
    process_manager: 'screen' | 'systemd';
    screen_session_name?: string;
    systemd_service_name?: string;
  };
  monitoring?: {
    enabled?: boolean;
    interval?: number;
    metrics?: string[];
    node_exporter_url?: string;
    node_exporter_port?: number;
  };
  dependencies?: {
    configured?: boolean;
    skip_update?: boolean;
    use_sudo?: boolean;
    create_user?: boolean;
    service_user?: string;
    service_groups?: string[];
    install_dir?: string;
  };
  backups?: {
    enabled?: boolean;
    schedule?: string;
    directories?: string[];
    retention?: {
      count?: number;
    };
    destinations?: {
      type: 'local' | 'sftp' | 's3';
      path?: string;
      endpoint?: string;
      bucket?: string;
      region?: string;
      path_prefix?: string;
      credentials_id?: string;
    }[];
  };
  runtime?: {
    java_xms?: string;
    java_xmx?: string;
    java_metaspace?: string;
    enable_string_dedup?: boolean;
    enable_aot?: boolean;
    enable_backup?: boolean;
    backup_dir?: string;
    backup_frequency?: string;
    assets_path?: string;
    extra_java_args?: string;
    extra_server_args?: string;
  };
  status?: ServerStatus;
  tags?: string[];
}

export interface ServerStatus {
  status: 'running' | 'stopped' | 'unknown' | 'starting' | 'stopping' | 'error';
  connection_status?: 'disconnected' | 'online' | 'running';
  uptime?: number;
  cpu_usage?: number;
  memory_usage?: number;
  memory_total?: number;
  player_count?: number;
  max_players?: number;
  last_check?: string;
  last_checked?: string;
  error_message?: string;
  health_check?: HealthCheck;
}

export interface HealthCheck {
  connection_status: 'disconnected' | 'online' | 'running';
  ssh: SSHHealthStatus;
  agent: AgentHealthStatus;
  process: ProcessHealthStatus;
  screen: ScreenHealthStatus;
}

export interface SSHHealthStatus {
  connected: boolean;
  error?: string;
  host: string;
  port: number;
}

export interface AgentHealthStatus {
  available: boolean;
  connected: boolean;
  error?: string;
  java_processes?: AgentJavaProcess[];
  listening_ports?: Record<number, boolean>;
  services?: Record<string, string>;
}

export interface ProcessHealthStatus {
  running: boolean;
  pid?: number;
  port?: string;
  uptime_seconds: number;
  detection_method?: string;
}

export interface ScreenHealthStatus {
  session_exists: boolean;
  session_name: string;
  streaming: boolean;
}

export interface ServerMetric {
  timestamp: string;
  cpu_usage?: number;
  memory_used?: number;
  memory_total?: number;
  disk_used?: number;
  disk_total?: number;
  network_rx?: number;
  network_tx?: number;
  status?: string;
}

export interface NodeExporterStatus {
  installed: boolean;
  running?: boolean;
  enabled?: boolean;
  version?: string;
  url?: string;
  output?: string;
}

export interface DependenciesCheckResponse {
  java_ok: boolean;
  java_line: string;
  user_ok: boolean;
  user_home: string;
  dir_ok: boolean;
  dir_path: string;
}

export interface AgentJavaProcess {
  pid: number;
  user: string;
  state: string;
  vsize: number;
  rss: number;
  utime_ticks: number;
  stime_ticks: number;
  start_ticks: number;
  cmdline: string;
  listen_ports: number[];
}

export interface AgentState {
  host_uuid: string;
  timestamp: number;
  services: Record<string, string>;
  ports: Record<string, boolean>;
  java: AgentJavaProcess[];
}

export interface ActivityLogEntry {
  timestamp: string;
  server_id: string;
  user_id?: number;
  activity_type: string;
  description: string;
  metadata?: Record<string, any>;
  success: boolean;
  error_message?: string;
}

export interface Backup {
  id: string;
  server_id: string;
  filename: string;
  size_bytes: number;
  created_at: string;
  destination_type: string;
  destination_path: string;
  status: 'creating' | 'completed' | 'failed' | 'deleted';
  error_message?: string;
  metadata?: Record<string, any>;
  created_by: string;
}

export interface BackupCompression {
  type: 'gzip' | 'none';
  level?: number;
}

export interface BackupDestination {
  type: 'local' | 'sftp' | 's3';
  path: string;
  sftp_host?: string;
  sftp_port?: number;
  sftp_username?: string;
  sftp_password?: string;
  sftp_key_path?: string;
  s3_bucket?: string;
  s3_region?: string;
  s3_access_key?: string;
  s3_secret_key?: string;
  s3_endpoint?: string;
}

export interface BackupSchedule {
  id: string;
  server_id: string;
  enabled: boolean;
  schedule: string;
  directories: string[];
  exclude: string[];
  destination: BackupDestination;
  retention_count: number;
  compression: BackupCompression;
  run_as_user?: string;
  use_sudo?: boolean;
  last_run?: string;
  next_run?: string;
}

export interface CreateBackupRequest {
  directories: string[];
  exclude?: string[];
  working_dir: string;
  destination: BackupDestination;
  compression?: BackupCompression;
  run_as_user?: string;
  use_sudo?: boolean;
}

export interface RestoreBackupRequest {
  destination: string;
}

export interface ConsoleMessage {
  type: 'output' | 'command' | 'error' | 'system';
  content: string;
  timestamp: string;
}

export interface ApiError {
  error: string;
  details?: string;
}

export interface PaginatedResponse<T> {
  data: T[];
  total: number;
  page: number;
  per_page: number;
}
