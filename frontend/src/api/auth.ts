import { apiClient } from './client';
import type { AuthResponse, User } from './types';

export interface LoginRequest {
  username: string;
  password: string;
}

export interface RegisterRequest {
  username: string;
  email: string;
  password: string;
  full_name: string;
}

export const authApi = {
  // Check if initial setup is required
  getSetupStatus: async (): Promise<{ requires_setup: boolean }> => {
    const response = await apiClient.get<{ requires_setup: boolean }>('/auth/setup-status');
    return response.data;
  },

  // Create initial admin user
  setupInitialAdmin: async (data: RegisterRequest): Promise<{ message: string }> => {
    const response = await apiClient.post<{ message: string }>('/auth/setup', data);
    return response.data;
  },

  // Login user
  login: async (credentials: LoginRequest): Promise<AuthResponse> => {
    const response = await apiClient.post<AuthResponse>('/auth/login', credentials);
    return response.data;
  },

  // Register new user
  register: async (data: RegisterRequest): Promise<AuthResponse> => {
    const response = await apiClient.post<AuthResponse>('/auth/register', data);
    return response.data;
  },

  // Logout user
  logout: async (): Promise<void> => {
    await apiClient.post('/auth/logout');
  },

  // Get current user
  getCurrentUser: async (): Promise<User> => {
    const response = await apiClient.get<User>('/auth/me');
    return response.data;
  },

  // Refresh token
  refreshToken: async (refreshToken?: string): Promise<AuthResponse> => {
    const payload = refreshToken ? { refresh_token: refreshToken } : undefined;
    const response = await apiClient.post<AuthResponse>('/auth/refresh', payload ?? null);
    return response.data;
  },
};
