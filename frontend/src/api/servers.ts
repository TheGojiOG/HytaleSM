import { apiClient } from './client';
import type { ActivityLogEntry, AgentState, DependenciesCheckResponse, NodeExporterStatus, Server, ServerMetric, ServerStatus } from './types';

export interface CreateServerRequest {
  id?: string;
  name: string;
  description?: string;
  connection: {
    host: string;
    port: number;
    username: string;
    auth_method: 'key' | 'password';
    key_path?: string;
    key_content?: string;
    password?: string;
  };
  server: {
    working_directory: string;
    executable: string;
    java_args?: string;
    process_manager: 'screen' | 'systemd';
  };
  monitoring?: {
    enabled?: boolean;
    interval?: number;
    metrics?: string[];
    node_exporter_url?: string;
    node_exporter_port?: number;
  };
}

export interface ExecuteCommandRequest {
  command: string;
}

export interface ProcessKillRequest {
  pid: number;
  use_sudo?: boolean;
}

export interface TestConnectionResponse {
  ok: boolean;
  user?: string;
  hostname?: string;
  os?: string;
  uptime?: string;
  host?: string;
  port?: number;
  metrics?: {
    cpu_usage?: number;
    memory_used?: number;
    memory_total?: number;
    disk_used?: number;
    disk_total?: number;
    network_rx?: number;
    network_tx?: number;
    load1?: number;
  };
  error?: string;
}

export interface StartServerRequest {
  install_dir?: string;
  service_user?: string;
  use_sudo?: boolean;
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
}

export const serversApi = {
  // List all servers
  listServers: async (): Promise<Server[]> => {
    const response = await apiClient.get<Server[]>('/servers');
    return response.data;
  },

  // Get server by ID
  getServer: async (id: string): Promise<Server> => {
    const response = await apiClient.get<Server>(`/servers/${id}`);
    return response.data;
  },

  // Create new server
  createServer: async (data: CreateServerRequest): Promise<Server> => {
    const response = await apiClient.post<Server>('/servers', data);
    return response.data;
  },

  // Update server
  updateServer: async (id: string, data: Partial<CreateServerRequest>): Promise<Server> => {
    const response = await apiClient.put<Server>(`/servers/${id}`, data);
    return response.data;
  },

  // Delete server
  deleteServer: async (id: string): Promise<void> => {
    await apiClient.delete(`/servers/${id}`);
  },

  // Start server
  startServer: async (id: string, data?: StartServerRequest): Promise<void> => {
    await apiClient.post(`/servers/${id}/start`, data);
  },

  // Stop server
  stopServer: async (id: string): Promise<void> => {
    await apiClient.post(`/servers/${id}/stop`);
  },

  // Restart server
  restartServer: async (id: string, data?: StartServerRequest): Promise<void> => {
    await apiClient.post(`/servers/${id}/restart`, data);
  },

  // Get server status
  getServerStatus: async (id: string): Promise<ServerStatus> => {
    const response = await apiClient.get<ServerStatus>(`/servers/${id}/status`);
    return response.data;
  },

  // Execute command
  executeCommand: async (id: string, data: ExecuteCommandRequest): Promise<void> => {
    await apiClient.post(`/servers/${id}/command`, data);
  },

  // Test SSH connection
  testConnection: async (id: string): Promise<TestConnectionResponse> => {
    const response = await apiClient.post<TestConnectionResponse>(`/servers/${id}/test-connection`);
    return response.data;
  },

  getMetricsHistory: async (id: string, limit = 50): Promise<ServerMetric[]> => {
    const response = await apiClient.get<{ metrics: ServerMetric[] }>(`/servers/${id}/metrics`, {
      params: { limit },
    });
    return response.data.metrics;
  },

  getLatestMetrics: async (): Promise<Record<string, ServerMetric>> => {
    const response = await apiClient.get<{ metrics: Record<string, ServerMetric> }>(`/servers/metrics/latest`);
    return response.data.metrics;
  },

  getLiveMetrics: async (): Promise<Record<string, ServerMetric>> => {
    const response = await apiClient.get<{ metrics: Record<string, ServerMetric> }>(`/servers/metrics/live`);
    return response.data.metrics;
  },

  getNodeExporterStatus: async (id: string): Promise<NodeExporterStatus> => {
    const response = await apiClient.get<NodeExporterStatus>(`/servers/${id}/node-exporter/status`);
    return response.data;
  },

  installNodeExporter: async (id: string): Promise<{ message: string }> => {
    const response = await apiClient.post<{ message: string }>(`/servers/${id}/node-exporter/install`);
    return response.data;
  },

  checkDependencies: async (id: string): Promise<DependenciesCheckResponse> => {
    const response = await apiClient.get<DependenciesCheckResponse>(`/servers/${id}/dependencies/check`);
    return response.data;
  },

  getAgentState: async (id: string): Promise<AgentState> => {
    const response = await apiClient.get<AgentState>(`/servers/${id}/agent/state`);
    return response.data;
  },

  killProcess: async (id: string, data: ProcessKillRequest): Promise<void> => {
    await apiClient.post(`/servers/${id}/processes/kill`, data);
  },

  getServerActivity: async (id: string, limit = 50, type?: string): Promise<ActivityLogEntry[]> => {
    const response = await apiClient.get<{ activities: ActivityLogEntry[] }>(`/servers/${id}/activity`, {
      params: { limit, type },
    });
    return response.data.activities;
  },
};
