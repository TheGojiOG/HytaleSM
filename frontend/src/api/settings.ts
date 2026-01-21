import { apiClient } from './client';

export interface AppSettings {
  security: {
    rate_limit: {
      enabled: boolean;
      requests_per_minute: number;
    };
    cors: {
      allowed_origins: string[];
      allowed_methods: string[];
    };
    ssh: {
      known_hosts_path: string;
      trust_on_first_use: boolean;
    };
  };
  logging: {
    level: string;
    format: string;
    file: string;
    max_size: number;
    max_backups: number;
    max_age: number;
  };
  metrics: {
    enabled: boolean;
    default_interval: number;
    retention_days: number;
  };
  requires_restart?: boolean;
}

export const settingsApi = {
  getSettings: async (): Promise<AppSettings> => {
    const response = await apiClient.get<AppSettings>('/settings');
    return response.data;
  },

  updateSettings: async (settings: AppSettings): Promise<AppSettings> => {
    const response = await apiClient.put<AppSettings>('/settings', settings);
    return response.data;
  },
};
