import { apiClient } from './client';
import type { Backup, BackupSchedule, CreateBackupRequest, RestoreBackupRequest } from './types';

export const backupsApi = {
  // List backups for a server
  listBackups: async (serverId: string): Promise<Backup[]> => {
    const response = await apiClient.get<{ backups: Backup[] }>(`/servers/${serverId}/backups`);
    return response.data.backups;
  },

  // Get backup details
  getBackup: async (serverId: string, backupId: string): Promise<Backup> => {
    const response = await apiClient.get<Backup>(`/servers/${serverId}/backups/${backupId}`);
    return response.data;
  },

  // Create backup
  createBackup: async (serverId: string, data: CreateBackupRequest): Promise<Backup> => {
    const response = await apiClient.post<{ backup: Backup }>(`/servers/${serverId}/backups`, data);
    return response.data.backup;
  },

  // Restore backup
  restoreBackup: async (serverId: string, backupId: string, data: RestoreBackupRequest): Promise<void> => {
    await apiClient.post(`/servers/${serverId}/backups/${backupId}/restore`, data);
  },

  // Delete backup
  deleteBackup: async (serverId: string, backupId: string): Promise<void> => {
    await apiClient.delete(`/servers/${serverId}/backups/${backupId}`);
  },

  // Enforce retention policy
  enforceRetention: async (serverId: string, keepCount: number): Promise<void> => {
    await apiClient.post(`/servers/${serverId}/backups/retention/enforce`, {
      retention_count: keepCount,
    });
  },
  
  getSchedule: async (serverId: string): Promise<BackupSchedule | null> => {
    const response = await apiClient.get<BackupSchedule>(`/servers/${serverId}/backups/schedule`, {
      validateStatus: (status) => (status >= 200 && status < 300) || status === 204,
    });
    if (response.status === 204) {
      return null;
    }
    return response.data;
  },

  listSchedules: async (serverId: string): Promise<BackupSchedule[]> => {
    const response = await apiClient.get<{ schedules: BackupSchedule[] }>(
      `/servers/${serverId}/backups/schedules`
    );
    return response.data.schedules;
  },

  updateSchedule: async (serverId: string, data: BackupSchedule): Promise<BackupSchedule> => {
    const response = await apiClient.put<BackupSchedule>(`/servers/${serverId}/backups/schedule`, data);
    return response.data;
  },

  initializeDefaultSchedule: async (serverId: string): Promise<BackupSchedule> => {
    const response = await apiClient.post<BackupSchedule>(`/servers/${serverId}/backups/schedule/default`);
    return response.data;
  },

  createSchedule: async (serverId: string, data: BackupSchedule): Promise<BackupSchedule> => {
    const response = await apiClient.post<BackupSchedule>(`/servers/${serverId}/backups/schedules`, data);
    return response.data;
  },

  updateScheduleById: async (serverId: string, scheduleId: string, data: BackupSchedule): Promise<BackupSchedule> => {
    const response = await apiClient.put<BackupSchedule>(
      `/servers/${serverId}/backups/schedules/${scheduleId}`,
      data
    );
    return response.data;
  },

  deleteSchedule: async (serverId: string): Promise<void> => {
    await apiClient.delete(`/servers/${serverId}/backups/schedule`);
  },

  deleteScheduleById: async (serverId: string, scheduleId: string): Promise<void> => {
    await apiClient.delete(`/servers/${serverId}/backups/schedules/${scheduleId}`);
  },

  getCron: async (serverId: string): Promise<{ user: string; lines: string[]; raw: string }> => {
    const response = await apiClient.get<{ user: string; lines: string[]; raw: string }>(
      `/servers/${serverId}/backups/cron`
    );
    return response.data;
  },
};
