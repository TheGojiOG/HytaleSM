import { apiClient } from './client';
import type { Permission, Role } from './types';

export async function listPermissions(): Promise<Permission[]> {
  const response = await apiClient.get<{ permissions: Permission[] }>('/iam/permissions');
  return response.data.permissions;
}

export async function listRoles(): Promise<Role[]> {
  const response = await apiClient.get<{ roles: Role[] }>('/iam/roles');
  return response.data.roles;
}

export async function getRole(roleId: number): Promise<Role> {
  const response = await apiClient.get<Role>(`/iam/roles/${roleId}`);
  return response.data;
}

export interface CreateRolePayload {
  name: string;
  description?: string;
  permission_names?: string[];
}

export async function createRole(payload: CreateRolePayload) {
  const response = await apiClient.post('/iam/roles', payload);
  return response.data as { id: number; message: string };
}

export interface UpdateRolePayload {
  name?: string;
  description?: string;
}

export async function updateRole(roleId: number, payload: UpdateRolePayload) {
  const response = await apiClient.put(`/iam/roles/${roleId}`, payload);
  return response.data as { message: string };
}

export async function deleteRole(roleId: number) {
  const response = await apiClient.delete(`/iam/roles/${roleId}`);
  return response.data as { message: string };
}

export async function setRolePermissions(roleId: number, permissionNames: string[]) {
  const response = await apiClient.put(`/iam/roles/${roleId}/permissions`, {
    permission_names: permissionNames,
  });
  return response.data as { message: string };
}
