import { useEffect, useMemo, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import { Button } from '@/components/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/Card';
import { Input } from '@/components/Input';
import { getErrorMessage } from '@/api/client';
import { assignUserRoles, createUser, deleteUser, listUsers, updateUser } from '@/api/users';
import { createRole, deleteRole, listPermissions, listRoles, setRolePermissions, updateRole } from '@/api/iam';
import type { Permission, Role, UserWithRoles } from '@/api/types';

export function UsersPage() {
  const [activeTab, setActiveTab] = useState<'users' | 'roles'>('users');
  const [users, setUsers] = useState<UserWithRoles[]>([]);
  const [roles, setRoles] = useState<Role[]>([]);
  const [permissions, setPermissions] = useState<Permission[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [selectedUserId, setSelectedUserId] = useState<number | null>(null);
  const [selectedRoleId, setSelectedRoleId] = useState<number | null>(null);

  const [newUser, setNewUser] = useState({ username: '', email: '', password: '' });
  const [userUpdate, setUserUpdate] = useState({ email: '', password: '', is_active: true });
  const [assignedRoleIds, setAssignedRoleIds] = useState<number[]>([]);

  const [newRole, setNewRole] = useState({ name: '', description: '' });
  const [newRolePermissions, setNewRolePermissions] = useState<string[]>([]);
  const [roleUpdate, setRoleUpdate] = useState({ name: '', description: '' });
  const [selectedPermissionNames, setSelectedPermissionNames] = useState<string[]>([]);

  const normalizeUsers = (data: UserWithRoles[]) =>
    data.map((user) => ({
      ...user,
      roles: user.roles ?? [],
    }));

  const selectedUser = useMemo(
    () => users.find((user) => user.id === selectedUserId) || null,
    [users, selectedUserId]
  );

  const selectedRole = useMemo(
    () => roles.find((role) => role.id === selectedRoleId) || null,
    [roles, selectedRoleId]
  );

  useEffect(() => {
    let isMounted = true;

    const loadData = async () => {
      try {
        setLoading(true);
        const [usersData, rolesData, permissionsData] = await Promise.all([
          listUsers(),
          listRoles(),
          listPermissions(),
        ]);
        if (!isMounted) {
          return;
        }
        setUsers(normalizeUsers(usersData));
        setRoles(rolesData);
        setPermissions(permissionsData);
      } catch (err) {
        if (!isMounted) {
          return;
        }
        setError(getErrorMessage(err));
      } finally {
        if (isMounted) {
          setLoading(false);
        }
      }
    };

    loadData();
    return () => {
      isMounted = false;
    };
  }, []);

  useEffect(() => {
    if (selectedUser) {
      setUserUpdate({
        email: selectedUser.email,
        password: '',
        is_active: selectedUser.is_active,
      });
      setAssignedRoleIds(selectedUser.roles.map((role) => role.id));
    }
  }, [selectedUser]);

  useEffect(() => {
    if (selectedRole) {
      setRoleUpdate({ name: selectedRole.name, description: selectedRole.description || '' });
      setSelectedPermissionNames(selectedRole.permissions || []);
    }
  }, [selectedRole]);

  const refreshUsers = async () => {
    const data = await listUsers();
    setUsers(normalizeUsers(data));
  };

  const refreshRoles = async () => {
    const data = await listRoles();
    setRoles(data);
  };

  const handleCreateUser = async () => {
    if (!newUser.username || !newUser.email || !newUser.password) {
      setError('Username, email, and password are required.');
      return;
    }

    setSaving(true);
    setError(null);
    try {
      await createUser(newUser);
      setNewUser({ username: '', email: '', password: '' });
      await refreshUsers();
    } catch (err) {
      setError(getErrorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  const handleUpdateUser = async () => {
    if (!selectedUser) {
      return;
    }

    setSaving(true);
    setError(null);
    try {
      await updateUser(selectedUser.id, {
        email: userUpdate.email || undefined,
        password: userUpdate.password || undefined,
        is_active: userUpdate.is_active,
      });
      await refreshUsers();
      setUserUpdate((prev) => ({ ...prev, password: '' }));
    } catch (err) {
      setError(getErrorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  const handleAssignRoles = async () => {
    if (!selectedUser) {
      return;
    }

    setSaving(true);
    setError(null);
    try {
      await assignUserRoles(selectedUser.id, assignedRoleIds);
      await refreshUsers();
    } catch (err) {
      setError(getErrorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  const handleDeleteUser = async () => {
    if (!selectedUser) {
      return;
    }

    setSaving(true);
    setError(null);
    try {
      await deleteUser(selectedUser.id);
      setSelectedUserId(null);
      await refreshUsers();
    } catch (err) {
      setError(getErrorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  const handleCreateRole = async () => {
    if (!newRole.name) {
      setError('Role name is required.');
      return;
    }

    setSaving(true);
    setError(null);
    try {
      await createRole({
        name: newRole.name,
        description: newRole.description,
        permission_names: newRolePermissions,
      });
      setNewRole({ name: '', description: '' });
      setNewRolePermissions([]);
      await refreshRoles();
    } catch (err) {
      setError(getErrorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  const handleUpdateRole = async () => {
    if (!selectedRole) {
      return;
    }

    setSaving(true);
    setError(null);
    try {
      await updateRole(selectedRole.id, {
        name: roleUpdate.name,
        description: roleUpdate.description,
      });
      await setRolePermissions(selectedRole.id, selectedPermissionNames);
      await refreshRoles();
    } catch (err) {
      setError(getErrorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  const handleDeleteRole = async () => {
    if (!selectedRole) {
      return;
    }

    setSaving(true);
    setError(null);
    try {
      await deleteRole(selectedRole.id);
      setSelectedRoleId(null);
      await refreshRoles();
    } catch (err) {
      setError(getErrorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  const toggleRoleAssignment = (roleId: number) => {
    setAssignedRoleIds((current) =>
      current.includes(roleId) ? current.filter((id) => id !== roleId) : [...current, roleId]
    );
  };

  const togglePermission = (
    permissionName: string,
    setter: Dispatch<SetStateAction<string[]>>
  ) => {
    setter((current) =>
      current.includes(permissionName)
        ? current.filter((name) => name !== permissionName)
        : [...current, permissionName]
    );
  };

  return (
    <div>
      <div className="mb-6">
        <h1 className="text-3xl font-bold text-white">Users & Roles</h1>
        <p className="text-neutral-400 mt-1">Manage user accounts, role assignments, and permissions</p>
      </div>

      <div className="flex flex-wrap gap-2 mb-6">
        <Button
          variant={activeTab === 'users' ? 'primary' : 'secondary'}
          size="sm"
          onClick={() => setActiveTab('users')}
        >
          Users
        </Button>
        <Button
          variant={activeTab === 'roles' ? 'primary' : 'secondary'}
          size="sm"
          onClick={() => setActiveTab('roles')}
        >
          Roles
        </Button>
      </div>

      {error && (
        <div className="mb-4 rounded-lg border border-red-500/40 bg-red-500/10 px-4 py-3 text-red-200">
          {error}
        </div>
      )}

      {loading ? (
        <Card>
          <CardContent className="py-10 text-neutral-400">Loading user management data…</CardContent>
        </Card>
      ) : activeTab === 'users' ? (
        <div className="grid grid-cols-1 xl:grid-cols-3 gap-6">
          <Card className="xl:col-span-2">
            <CardHeader>
              <CardTitle>Users</CardTitle>
              <CardDescription>All registered users and their current roles.</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="overflow-x-auto">
                <table className="w-full text-left text-sm">
                  <thead className="text-neutral-400">
                    <tr>
                      <th className="py-2">User</th>
                      <th className="py-2">Email</th>
                      <th className="py-2">Status</th>
                      <th className="py-2">Roles</th>
                      <th className="py-2"></th>
                    </tr>
                  </thead>
                  <tbody className="text-neutral-200">
                    {users.map((user) => (
                      <tr key={user.id} className="border-t border-neutral-800">
                        <td className="py-2">
                          <div className="font-medium text-white">{user.username}</div>
                          <div className="text-xs text-neutral-500">ID {user.id}</div>
                        </td>
                        <td className="py-2">{user.email}</td>
                        <td className="py-2">
                          <span className={user.is_active ? 'text-emerald-400' : 'text-red-400'}>
                            {user.is_active ? 'Active' : 'Disabled'}
                          </span>
                        </td>
                        <td className="py-2">
                          {user.roles.length === 0 ? (
                            <span className="text-neutral-500">No roles</span>
                          ) : (
                            <div className="flex flex-wrap gap-1">
                              {user.roles.map((role) => (
                                <span
                                  key={role.id}
                                  className="rounded-full bg-neutral-800 px-2 py-0.5 text-xs text-neutral-200"
                                >
                                  {role.name}
                                </span>
                              ))}
                            </div>
                          )}
                        </td>
                        <td className="py-2 text-right">
                          <Button
                            size="sm"
                            variant={selectedUserId === user.id ? 'primary' : 'secondary'}
                            onClick={() => setSelectedUserId(user.id)}
                          >
                            Manage
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </CardContent>
          </Card>

          <div className="flex flex-col gap-6">
            <Card>
              <CardHeader>
                <CardTitle>Create User</CardTitle>
                <CardDescription>Add a new user with the Viewer role by default.</CardDescription>
              </CardHeader>
              <CardContent className="space-y-3">
                <Input
                  label="Username"
                  value={newUser.username}
                  onChange={(event) => setNewUser((prev) => ({ ...prev, username: event.target.value }))}
                />
                <Input
                  label="Email"
                  type="email"
                  value={newUser.email}
                  onChange={(event) => setNewUser((prev) => ({ ...prev, email: event.target.value }))}
                />
                <Input
                  label="Password"
                  type="password"
                  value={newUser.password}
                  onChange={(event) => setNewUser((prev) => ({ ...prev, password: event.target.value }))}
                />
                <Button isLoading={saving} onClick={handleCreateUser}>
                  Create User
                </Button>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Selected User</CardTitle>
                <CardDescription>Update profile details and role assignments.</CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                {!selectedUser ? (
                  <p className="text-neutral-400">Select a user to manage.</p>
                ) : (
                  <>
                    <div className="text-sm text-neutral-400">
                      Managing <span className="text-white font-medium">{selectedUser.username}</span>
                    </div>
                    <Input
                      label="Email"
                      type="email"
                      value={userUpdate.email}
                      onChange={(event) => setUserUpdate((prev) => ({ ...prev, email: event.target.value }))}
                    />
                    <Input
                      label="New Password"
                      type="password"
                      value={userUpdate.password}
                      onChange={(event) => setUserUpdate((prev) => ({ ...prev, password: event.target.value }))}
                      placeholder="Leave blank to keep current"
                    />
                    <label className="flex items-center gap-2 text-sm text-neutral-300">
                      <input
                        type="checkbox"
                        checked={userUpdate.is_active}
                        onChange={(event) =>
                          setUserUpdate((prev) => ({ ...prev, is_active: event.target.checked }))
                        }
                      />
                      Active account
                    </label>
                    <Button isLoading={saving} onClick={handleUpdateUser}>
                      Save User
                    </Button>

                    <div className="border-t border-neutral-800 pt-4">
                      <div className="text-sm font-medium text-white mb-2">Assign Roles</div>
                      <div className="space-y-2 max-h-48 overflow-y-auto">
                        {roles.map((role) => (
                          <label key={role.id} className="flex items-center gap-2 text-sm text-neutral-300">
                            <input
                              type="checkbox"
                              checked={assignedRoleIds.includes(role.id)}
                              onChange={() => toggleRoleAssignment(role.id)}
                            />
                            <span>{role.name}</span>
                          </label>
                        ))}
                      </div>
                      <Button className="mt-3" variant="secondary" isLoading={saving} onClick={handleAssignRoles}>
                        Update Roles
                      </Button>
                    </div>

                    <div className="border-t border-neutral-800 pt-4">
                      <Button variant="danger" isLoading={saving} onClick={handleDeleteUser}>
                        Delete User
                      </Button>
                    </div>
                  </>
                )}
              </CardContent>
            </Card>
          </div>
        </div>
      ) : (
        <div className="grid grid-cols-1 xl:grid-cols-3 gap-6">
          <Card className="xl:col-span-2">
            <CardHeader>
              <CardTitle>Roles</CardTitle>
              <CardDescription>Review predefined roles and permissions.</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                {roles.map((role) => (
                  <button
                    key={role.id}
                    onClick={() => setSelectedRoleId(role.id)}
                    className={`rounded-lg border px-4 py-3 text-left transition-colors ${
                      selectedRoleId === role.id
                        ? 'border-emerald-500 bg-emerald-500/10'
                        : 'border-neutral-800 bg-neutral-900 hover:border-neutral-600'
                    }`}
                  >
                    <div className="text-white font-medium">{role.name}</div>
                    <div className="text-xs text-neutral-400">{role.description || 'No description'}</div>
                    <div className="mt-2 text-xs text-neutral-500">
                      {role.permissions.length} permissions
                    </div>
                  </button>
                ))}
              </div>
            </CardContent>
          </Card>

          <div className="flex flex-col gap-6">
            <Card>
              <CardHeader>
                <CardTitle>Create Role</CardTitle>
                <CardDescription>Define a new role and assign permissions.</CardDescription>
              </CardHeader>
              <CardContent className="space-y-3">
                <Input
                  label="Role name"
                  value={newRole.name}
                  onChange={(event) => setNewRole((prev) => ({ ...prev, name: event.target.value }))}
                />
                <Input
                  label="Description"
                  value={newRole.description}
                  onChange={(event) => setNewRole((prev) => ({ ...prev, description: event.target.value }))}
                />
                <div className="text-sm font-medium text-neutral-300">Permissions</div>
                <div className="max-h-48 overflow-y-auto space-y-2 text-sm text-neutral-300">
                  {permissions.map((permission) => (
                    <label key={permission.name} className="flex items-center gap-2">
                      <input
                        type="checkbox"
                        checked={newRolePermissions.includes(permission.name)}
                        onChange={() => togglePermission(permission.name, setNewRolePermissions)}
                      />
                      <span>{permission.name}</span>
                    </label>
                  ))}
                </div>
                <Button isLoading={saving} onClick={handleCreateRole}>
                  Create Role
                </Button>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Selected Role</CardTitle>
                <CardDescription>Edit role details and permissions.</CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                {!selectedRole ? (
                  <p className="text-neutral-400">Select a role to manage.</p>
                ) : (
                  <>
                    <Input
                      label="Role name"
                      value={roleUpdate.name}
                      onChange={(event) => setRoleUpdate((prev) => ({ ...prev, name: event.target.value }))}
                    />
                    <Input
                      label="Description"
                      value={roleUpdate.description}
                      onChange={(event) => setRoleUpdate((prev) => ({ ...prev, description: event.target.value }))}
                    />
                    <div className="text-sm font-medium text-neutral-300">Permissions</div>
                    <div className="max-h-48 overflow-y-auto space-y-2 text-sm text-neutral-300">
                      {permissions.map((permission) => (
                        <label key={permission.name} className="flex items-center gap-2">
                          <input
                            type="checkbox"
                            checked={selectedPermissionNames.includes(permission.name)}
                            onChange={() => togglePermission(permission.name, setSelectedPermissionNames)}
                          />
                          <span>{permission.name}</span>
                        </label>
                      ))}
                    </div>
                    <Button isLoading={saving} onClick={handleUpdateRole}>
                      Save Role
                    </Button>
                    <Button variant="danger" isLoading={saving} onClick={handleDeleteRole}>
                      Delete Role
                    </Button>
                  </>
                )}
              </CardContent>
            </Card>
          </div>
        </div>
      )}
    </div>
  );
}
