import { apiClient } from './client';

export interface Release {
  id: number;
  version: string;
  patchline: string;
  file_path: string;
  file_size: number;
  sha256: string;
  downloader_version?: string;
  downloaded_at: string;
  status: string;
  source?: string;
  removed?: boolean;
}

export interface ReleaseJob {
  id: string;
  action: string;
  status: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
  output: string[];
  error?: string;
  needs_auth?: boolean;
  auth_url?: string;
  auth_code?: string;
}

export interface ReleaseRequest {
  patchline?: string;
  download_path?: string;
}

export interface DownloaderInitRequest {
  force?: boolean;
}

export interface DownloaderStatusResponse {
  exists: boolean;
  path: string;
}

export interface DownloaderAuthStatusResponse {
  exists: boolean;
  expires_at?: number;
  branch?: string;
}

export const releasesApi = {
  listReleases: async (includeRemoved = false): Promise<Release[]> => {
    const response = await apiClient.get<{ releases: Release[] }>('/releases', {
      params: includeRemoved ? { include_removed: 'true' } : undefined,
    });
    return response.data.releases;
  },

  getRelease: async (id: number): Promise<Release> => {
    const response = await apiClient.get<Release>(`/releases/${id}`);
    return response.data;
  },

  listJobs: async (): Promise<ReleaseJob[]> => {
    const response = await apiClient.get<{ jobs: ReleaseJob[] }>('/releases/jobs');
    return response.data.jobs;
  },

  getJob: async (id: string): Promise<ReleaseJob> => {
    const response = await apiClient.get<{ job: ReleaseJob }>(`/releases/jobs/${id}`);
    return response.data.job;
  },

  downloadRelease: async (payload: ReleaseRequest): Promise<ReleaseJob> => {
    const response = await apiClient.post<{ job: ReleaseJob }>('/releases/download', payload);
    return response.data.job;
  },

  initDownloader: async (payload?: DownloaderInitRequest): Promise<ReleaseJob> => {
    const response = await apiClient.post<{ job: ReleaseJob }>('/releases/downloader/init', payload ?? {});
    return response.data.job;
  },

  printVersion: async (payload: ReleaseRequest): Promise<ReleaseJob> => {
    const response = await apiClient.post<{ job: ReleaseJob }>('/releases/print-version', payload);
    return response.data.job;
  },

  checkUpdate: async (): Promise<ReleaseJob> => {
    const response = await apiClient.post<{ job: ReleaseJob }>('/releases/check-update');
    return response.data.job;
  },

  downloaderVersion: async (): Promise<ReleaseJob> => {
    const response = await apiClient.post<{ job: ReleaseJob }>('/releases/downloader-version');
    return response.data.job;
  },

  downloaderStatus: async (): Promise<DownloaderStatusResponse> => {
    const response = await apiClient.get<DownloaderStatusResponse>('/releases/downloader/status');
    return response.data;
  },

  downloaderAuthStatus: async (): Promise<DownloaderAuthStatusResponse> => {
    const response = await apiClient.get<DownloaderAuthStatusResponse>('/releases/downloader/auth');
    return response.data;
  },

  resetAuth: async (): Promise<void> => {
    await apiClient.post('/releases/reset-auth');
  },

  deleteRelease: async (id: number): Promise<void> => {
    await apiClient.delete(`/releases/${id}`);
  },
};
