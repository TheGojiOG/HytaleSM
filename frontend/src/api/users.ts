import { apiClient } from './client';
import type { UserWithRoles } from './types';

export async function listUsers(): Promise<UserWithRoles[]> {
  const response = await apiClient.get<UserWithRoles[]>('/users');
  return response.data;
}

export interface CreateUserPayload {
  username: string;
  email: string;
  password: string;
}

export async function createUser(payload: CreateUserPayload) {
  const response = await apiClient.post('/users', payload);
  return response.data as { id: number; message: string };
}

export interface UpdateUserPayload {
  email?: string;
  password?: string;
  is_active?: boolean;
}

export async function updateUser(userId: number, payload: UpdateUserPayload) {
  const response = await apiClient.put(`/users/${userId}`, payload);
  return response.data as { message: string };
}

export async function deleteUser(userId: number) {
  const response = await apiClient.delete(`/users/${userId}`);
  return response.data as { message: string };
}

export async function assignUserRoles(userId: number, roleIds: number[]) {
  const response = await apiClient.put(`/users/${userId}/roles`, { role_ids: roleIds });
  return response.data as { message: string };
}
