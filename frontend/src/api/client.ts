import axios, { AxiosError } from 'axios';
import type { ApiError } from './types';

// Create axios instance with base configuration
export const apiClient = axios.create({
  baseURL: '/api/v1',
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
  },
  withCredentials: true,
});

// Response interceptor - handle token refresh and errors
apiClient.interceptors.response.use(
  (response) => response,
  async (error: AxiosError<ApiError>) => {
    const originalRequest = error.config as any;
    const requestUrl = (originalRequest?.url ?? '') as string;
    const isAuthEndpoint = requestUrl.includes('/auth/login') ||
      requestUrl.includes('/auth/register') ||
      requestUrl.includes('/auth/refresh') ||
      requestUrl.includes('/auth/setup') ||
      requestUrl.includes('/auth/setup-status');
    const isAuthMe = requestUrl.includes('/auth/me');

    // Handle 401 unauthorized - attempt token refresh
    if (error.response?.status === 401 && !originalRequest._retry && !isAuthEndpoint && !isAuthMe) {
      originalRequest._retry = true;

      try {
        await axios.post('/api/v1/auth/refresh', null, { withCredentials: true });

        // Retry original request with new token
        return apiClient(originalRequest);
      } catch (refreshError) {
        // Refresh failed, redirect to login
        window.location.href = '/login';
        return Promise.reject(refreshError);
      }
    }

    return Promise.reject(error);
  }
);

// Helper function to extract error message
export function getErrorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const axiosError = error as AxiosError<ApiError>;
    return axiosError.response?.data?.error || axiosError.message || 'An unexpected error occurred';
  }
  if (error instanceof Error) {
    return error.message;
  }
  return 'An unexpected error occurred';
}
