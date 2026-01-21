import { useMemo, useState } from 'react';
import type { FormEvent } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { serversApi } from '@/api';
import type { CreateServerRequest } from '@/api/servers';
import type { Server as ServerType } from '@/api/types';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Input } from '@/components/Input';
import { Server, Plus, Play, Square, RotateCw, Terminal, Database } from 'lucide-react';
import { formatBytes, formatRelativeTime } from '@/utils/format';
import { Link } from 'react-router-dom';
import { StatusBadge } from '@/components/StatusBadge';

type CreateFormState = CreateServerRequest & {
  monitoring: NonNullable<CreateServerRequest['monitoring']>;
};

export function ServersPage() {
  const queryClient = useQueryClient();
  const [actionState, setActionState] = useState<Record<string, { action?: 'start' | 'stop' | 'restart'; error?: string }>>({});
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [viewMode, setViewMode] = useState<'tiles' | 'list'>('tiles');
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [showKey, setShowKey] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [createLoading, setCreateLoading] = useState(false);
  const [createForm, setCreateForm] = useState<CreateFormState>({
    name: '',
    description: '',
    connection: {
      host: '',
      port: 22,
      username: '',
      auth_method: 'key',
      key_path: '',
      key_content: '',
    },
    server: {
      working_directory: '',
      executable: '',
      java_args: '',
      process_manager: 'screen',
    },
    monitoring: {
      enabled: false,
      node_exporter_url: '',
      node_exporter_port: 9100,
    },
  });
  const { data: servers, isLoading, error } = useQuery({
    queryKey: ['servers'],
    queryFn: serversApi.listServers,
    refetchInterval: 15000, // Refresh every 15 seconds
  });
  const { data: latestMetrics } = useQuery({
    queryKey: ['servers-latest-metrics'],
    queryFn: serversApi.getLatestMetrics,
    enabled: Boolean(servers && servers.length > 0),
  });
  const { data: liveMetrics } = useQuery({
    queryKey: ['servers-live-metrics'],
    queryFn: serversApi.getLiveMetrics,
    enabled: Boolean(servers && servers.length > 0),
    refetchInterval: 15000, // Refresh every 15 seconds
  });

  const isOnline = (status?: string) => status === 'running' || status === 'online';
  const canStart = (status?: string) => !isOnline(status);
  const canStop = (status?: string) => isOnline(status) || status === 'starting';
  const canRestart = (status?: string) => isOnline(status) || status === 'starting';
  const getHost = (server: ServerType) => server.connection?.host ?? server.host ?? 'unknown';
  const getPort = (server: ServerType) => server.connection?.port ?? server.port ?? 0;
  const getStatus = (server: ServerType) => server.status?.status ?? 'unknown';

  const getErrorMessage = (err: unknown, fallback: string) => {
    const maybe = err as { response?: { data?: { error?: string } } };
    return maybe?.response?.data?.error ?? fallback;
  };

  const getLatestMetric = (serverId: string) => liveMetrics?.[serverId] ?? latestMetrics?.[serverId];

  const executeAction = async (
    serverId: string,
    action: 'start' | 'stop' | 'restart',
    refresh = true
  ) => {
    setActionState((prev) => ({
      ...prev,
      [serverId]: { action, error: undefined },
    }));

    try {
      if (action === 'start') {
        await serversApi.startServer(serverId);
      } else if (action === 'stop') {
        await serversApi.stopServer(serverId);
      } else {
        await serversApi.restartServer(serverId);
      }

      if (refresh) {
        await queryClient.invalidateQueries({ queryKey: ['servers'] });
      }
    } catch (err: unknown) {
      const message = getErrorMessage(err, 'Action failed. Please try again.');
      setActionState((prev) => ({
        ...prev,
        [serverId]: { action: undefined, error: message },
      }));
      return;
    }

    setActionState((prev) => ({
      ...prev,
      [serverId]: { action: undefined, error: undefined },
    }));
  };

  const runAction = async (serverId: string, action: 'start' | 'stop' | 'restart') => {
    if (action === 'stop') {
      const confirmed = window.confirm('Stop this server?');
      if (!confirmed) {
        return;
      }
    }

    if (action === 'restart') {
      const confirmed = window.confirm('Restart this server?');
      if (!confirmed) {
        return;
      }
    }

    await executeAction(serverId, action);
  };

  const handleBulkAction = async (action: 'start' | 'stop' | 'restart' | 'delete') => {
    if (!servers || selectedIds.size === 0) {
      return;
    }

    const selectedServers = servers.filter((server) => selectedIds.has(server.id));
      const eligibleServers = selectedServers.filter((server) => {
        const status = getStatus(server);
        if (action === 'start') {
          return canStart(status);
        }
        if (action === 'stop') {
          return canStop(status);
        }
        if (action === 'restart') {
          return canRestart(status);
        }
        return true;
      });

    if (eligibleServers.length === 0) {
      return;
    }

    if (action === 'start') {
      const confirmed = window.confirm(`Start ${eligibleServers.length} selected server(s)?`);
      if (!confirmed) {
        return;
      }
    }

    if (action === 'stop') {
      const confirmed = window.confirm(`Stop ${eligibleServers.length} selected server(s)?`);
      if (!confirmed) {
        return;
      }
    }

    if (action === 'restart') {
      const confirmed = window.confirm(`Restart ${eligibleServers.length} selected server(s)?`);
      if (!confirmed) {
        return;
      }
    }

    if (action === 'delete') {
      const confirmed = window.confirm(`Delete ${eligibleServers.length} selected server(s)? This cannot be undone.`);
      if (!confirmed) {
        return;
      }

      await Promise.allSettled(
        eligibleServers.map(async (server) => {
          try {
            await serversApi.deleteServer(server.id);
            setActionState((prev) => ({
              ...prev,
              [server.id]: { action: undefined, error: undefined },
            }));
          } catch (err: unknown) {
            const message = getErrorMessage(err, 'Delete failed.');
            setActionState((prev) => ({
              ...prev,
              [server.id]: { action: undefined, error: message },
            }));
          }
        })
      );

      setSelectedIds(new Set());
      await queryClient.invalidateQueries({ queryKey: ['servers'] });
      return;
    }

    await Promise.allSettled(
      eligibleServers.map((server) => executeAction(server.id, action, false))
    );
    await queryClient.invalidateQueries({ queryKey: ['servers'] });
  };

  const selectedCount = selectedIds.size;
  const eligibleCounts = useMemo(() => {
    if (!servers) {
      return { start: 0, stop: 0, restart: 0, delete: 0 };
    }
    const selectedServers = servers.filter((server) => selectedIds.has(server.id));
    return {
      start: selectedServers.filter((server) => canStart(getStatus(server))).length,
      stop: selectedServers.filter((server) => canStop(getStatus(server))).length,
      restart: selectedServers.filter((server) => canRestart(getStatus(server))).length,
      delete: selectedServers.length,
    };
  }, [selectedIds, servers]);

  const toggleSelectAll = () => {
    if (!servers || servers.length === 0) {
      return;
    }
    if (selectedIds.size === servers.length) {
      setSelectedIds(new Set());
      return;
    }
    setSelectedIds(new Set(servers.map((server) => server.id)));
  };

  const toggleSelect = (serverId: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(serverId)) {
        next.delete(serverId);
      } else {
        next.add(serverId);
      }
      return next;
    });
  };

  const handleDelete = async (serverId: string) => {
    const confirmed = window.confirm('Delete this server? This cannot be undone.');
    if (!confirmed) {
      return;
    }

    setActionState((prev) => ({
      ...prev,
      [serverId]: { action: undefined, error: undefined },
    }));

    try {
      await serversApi.deleteServer(serverId);
      setSelectedIds((prev) => {
        const next = new Set(prev);
        next.delete(serverId);
        return next;
      });
      await queryClient.invalidateQueries({ queryKey: ['servers'] });
    } catch (err: unknown) {
      const message = getErrorMessage(err, 'Delete failed.');
      setActionState((prev) => ({
        ...prev,
        [serverId]: { action: undefined, error: message },
      }));
    }
  };

  const handleCreateServer = async (event: FormEvent) => {
    event.preventDefault();
    setCreateError(null);

    if (!createForm.name || !createForm.connection.host || !createForm.connection.username) {
      setCreateError('Name, host, and username are required.');
      return;
    }

    if (!createForm.connection.key_content) {
      setCreateError('SSH key content is required.');
      return;
    }

    if (showAdvanced && (!createForm.server.working_directory || !createForm.server.executable)) {
      setCreateError('Working directory and executable are required when advanced options are enabled.');
      return;
    }

    setCreateLoading(true);
    try {
      await serversApi.createServer({
        name: createForm.name,
        description: createForm.description,
        connection: {
          host: createForm.connection.host,
          port: Number(createForm.connection.port) || 22,
          username: createForm.connection.username,
          auth_method: createForm.connection.auth_method,
          key_content: createForm.connection.key_content,
        },
        server: {
          working_directory: createForm.server.working_directory,
          executable: createForm.server.executable,
          java_args: createForm.server.java_args,
          process_manager: createForm.server.process_manager,
        },
        monitoring: {
          enabled: createForm.monitoring.enabled,
          node_exporter_url: createForm.monitoring.node_exporter_url || undefined,
          node_exporter_port: createForm.monitoring.node_exporter_port || undefined,
        },
      });

      setCreateForm({
        name: '',
        description: '',
        connection: {
          host: '',
          port: 22,
          username: '',
          auth_method: 'key',
          key_path: '',
          key_content: '',
        },
        server: {
          working_directory: '',
          executable: '',
          java_args: '',
          process_manager: 'screen',
        },
        monitoring: {
          enabled: false,
          node_exporter_url: '',
          node_exporter_port: 9100,
        },
      });
      setShowCreateForm(false);
      await queryClient.invalidateQueries({ queryKey: ['servers'] });
    } catch (err: unknown) {
      setCreateError(getErrorMessage(err, 'Failed to create server.'));
    } finally {
      setCreateLoading(false);
    }
  };

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-neutral-400">Loading servers...</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-red-400">Failed to load servers</div>
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-3xl font-bold text-white">Servers</h1>
          <p className="text-neutral-400 mt-1">Manage your Hytale server instances</p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant={viewMode === 'tiles' ? 'primary' : 'secondary'}
            size="sm"
            onClick={() => setViewMode('tiles')}
          >
            Tiles
          </Button>
          <Button
            variant={viewMode === 'list' ? 'primary' : 'secondary'}
            size="sm"
            onClick={() => setViewMode('list')}
          >
            List
          </Button>
          <Button variant="primary" onClick={() => setShowCreateForm((prev) => !prev)}>
            <Plus className="w-4 h-4 mr-2" />
            {showCreateForm ? 'Close Form' : 'Add Server'}
          </Button>
        </div>
      </div>

      {showCreateForm && (
        <Card className="mb-6">
          <CardHeader>
            <CardTitle>Add Server</CardTitle>
            <CardDescription>Provide SSH key access and basic connection details.</CardDescription>
          </CardHeader>
          <CardContent>
            <form onSubmit={handleCreateServer} className="space-y-4">
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <Input
                  label="Server Name"
                  value={createForm.name}
                  onChange={(event) => setCreateForm((prev) => ({ ...prev, name: event.target.value }))}
                  placeholder="Hytale Alpha"
                />
                <Input
                  label="Description"
                  value={createForm.description}
                  onChange={(event) => setCreateForm((prev) => ({ ...prev, description: event.target.value }))}
                  placeholder="Optional description"
                />
              </div>

              <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Input
                  label="Host"
                  value={createForm.connection.host}
                  onChange={(event) =>
                    setCreateForm((prev) => ({
                      ...prev,
                      connection: { ...prev.connection, host: event.target.value },
                    }))
                  }
                  placeholder="server.example.com"
                />
                <Input
                  label="SSH Port"
                  type="number"
                  value={createForm.connection.port}
                  onChange={(event) =>
                    setCreateForm((prev) => ({
                      ...prev,
                      connection: { ...prev.connection, port: Number(event.target.value) },
                    }))
                  }
                />
                <Input
                  label="SSH Username"
                  value={createForm.connection.username}
                  onChange={(event) =>
                    setCreateForm((prev) => ({
                      ...prev,
                      connection: { ...prev.connection, username: event.target.value },
                    }))
                  }
                />
              </div>

              <div>
                <div className="flex items-center justify-between mb-1.5">
                  <label className="block text-sm font-medium text-neutral-300">SSH Private Key</label>
                  <button
                    type="button"
                    onClick={() => setShowKey((prev) => !prev)}
                    className="text-xs text-neutral-400 hover:text-neutral-200"
                  >
                    {showKey ? 'Hide' : 'Show'}
                  </button>
                </div>
                <textarea
                  className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white placeholder-neutral-500 focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:border-transparent"
                  rows={6}
                  placeholder="Paste the full SSH private key here"
                  style={!showKey ? ({ WebkitTextSecurity: 'disc' } as React.CSSProperties) : undefined}
                  value={createForm.connection.key_content}
                  onChange={(event) =>
                    setCreateForm((prev) => ({
                      ...prev,
                      connection: { ...prev.connection, key_content: event.target.value },
                    }))
                  }
                />
                <p className="text-xs text-neutral-500 mt-1">Keys are stored server-side and used for SSH connections.</p>
              </div>

              <div className="border border-neutral-800 rounded-lg p-4">
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-sm font-medium text-neutral-200">Advanced settings</p>
                    <p className="text-xs text-neutral-500">For pre-defined Java game instances (paths, args, process manager).</p>
                  </div>
                  <Button
                    type="button"
                    variant="secondary"
                    size="sm"
                    onClick={() => setShowAdvanced((prev) => !prev)}
                  >
                    {showAdvanced ? 'Hide' : 'Show'}
                  </Button>
                </div>

                {showAdvanced && (
                  <div className="mt-4 space-y-4">
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                      <Input
                        label="Working Directory"
                        value={createForm.server.working_directory}
                        onChange={(event) =>
                          setCreateForm((prev) => ({
                            ...prev,
                            server: { ...prev.server, working_directory: event.target.value },
                          }))
                        }
                        placeholder="/srv/hytale"
                      />
                      <Input
                        label="Executable"
                        value={createForm.server.executable}
                        onChange={(event) =>
                          setCreateForm((prev) => ({
                            ...prev,
                            server: { ...prev.server, executable: event.target.value },
                          }))
                        }
                        placeholder="java"
                      />
                    </div>

                    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                      <Input
                        label="Java Args"
                        value={createForm.server.java_args}
                        onChange={(event) =>
                          setCreateForm((prev) => ({
                            ...prev,
                            server: { ...prev.server, java_args: event.target.value },
                          }))
                        }
                        placeholder="-Xmx2G"
                      />
                      <div>
                        <label className="block text-sm font-medium text-neutral-300 mb-1.5">Process Manager</label>
                        <select
                          className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white"
                          value={createForm.server.process_manager}
                          onChange={(event) =>
                            setCreateForm((prev) => ({
                              ...prev,
                              server: { ...prev.server, process_manager: event.target.value as 'screen' | 'systemd' },
                            }))
                          }
                        >
                          <option value="screen">screen</option>
                          <option value="systemd">systemd</option>
                        </select>
                      </div>
                    </div>
                  </div>
                )}
              </div>

              <div className="border border-neutral-800 rounded-lg p-4">
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-sm font-medium text-neutral-200">Monitoring (node_exporter)</p>
                    <p className="text-xs text-neutral-500">Pull metrics from the server's node_exporter endpoint.</p>
                  </div>
                  <label className="text-xs text-neutral-400 flex items-center gap-2">
                    <input
                      type="checkbox"
                      className="accent-emerald-500"
                      checked={createForm.monitoring.enabled}
                      onChange={(event) =>
                        setCreateForm((prev) => ({
                          ...prev,
                          monitoring: { ...prev.monitoring, enabled: event.target.checked },
                        }))
                      }
                    />
                    Enabled
                  </label>
                </div>
                <div className="mt-4 grid grid-cols-1 md:grid-cols-2 gap-4">
                  <Input
                    label="Node exporter URL"
                    value={createForm.monitoring.node_exporter_url}
                    onChange={(event) =>
                      setCreateForm((prev) => ({
                        ...prev,
                        monitoring: { ...prev.monitoring, node_exporter_url: event.target.value },
                      }))
                    }
                    placeholder="http://server:9100/metrics"
                  />
                  <Input
                    label="Node exporter Port"
                    type="number"
                    value={createForm.monitoring.node_exporter_port}
                    onChange={(event) =>
                      setCreateForm((prev) => ({
                        ...prev,
                        monitoring: { ...prev.monitoring, node_exporter_port: Number(event.target.value) },
                      }))
                    }
                    placeholder="9100"
                  />
                </div>
                <p className="text-xs text-neutral-500 mt-2">
                  If URL is empty, the host and port are combined as http://&lt;host&gt;:&lt;port&gt;/metrics.
                </p>
              </div>

              {createError && <p className="text-sm text-red-400">{createError}</p>}

              <div className="flex justify-end gap-2">
                <Button variant="secondary" type="button" onClick={() => setShowCreateForm(false)}>
                  Cancel
                </Button>
                <Button variant="primary" type="submit" isLoading={createLoading}>
                  Create Server
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      )}

      {selectedCount > 0 && (
        <Card className="mb-6">
          <CardContent className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
            <div className="text-sm text-neutral-300">
              {selectedCount} selected • startable {eligibleCounts.start} • stoppable {eligibleCounts.stop} • restartable {eligibleCounts.restart}
            </div>
            <div className="flex flex-wrap gap-2">
              <Button
                variant="secondary"
                size="sm"
                disabled={eligibleCounts.start === 0}
                onClick={() => handleBulkAction('start')}
              >
                <Play className="w-4 h-4 mr-1" />
                Start
              </Button>
              <Button
                variant="secondary"
                size="sm"
                disabled={eligibleCounts.stop === 0}
                onClick={() => handleBulkAction('stop')}
              >
                <Square className="w-4 h-4 mr-1" />
                Stop
              </Button>
              <Button
                variant="secondary"
                size="sm"
                disabled={eligibleCounts.restart === 0}
                onClick={() => handleBulkAction('restart')}
              >
                <RotateCw className="w-4 h-4 mr-1" />
                Restart
              </Button>
              <Button
                variant="danger"
                size="sm"
                disabled={eligibleCounts.delete === 0}
                onClick={() => handleBulkAction('delete')}
              >
                Delete
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      {!servers || servers.length === 0 ? (
        <Card>
          <CardContent className="flex flex-col items-center justify-center py-12">
            <div className="w-16 h-16 bg-neutral-800 rounded-full flex items-center justify-center mb-4">
              <Server className="w-8 h-8 text-neutral-400" />
            </div>
            <h3 className="text-lg font-semibold text-white mb-2">No servers yet</h3>
            <p className="text-neutral-400 text-center mb-6 max-w-md">
              Get started by adding your first Hytale server to manage it from this dashboard.
            </p>
            <Button variant="primary" onClick={() => setShowCreateForm(true)}>
              <Plus className="w-4 h-4 mr-2" />
              Add Your First Server
            </Button>
          </CardContent>
        </Card>
      ) : (
        viewMode === 'tiles' ? (
          <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-6">
            {servers.map((server) => (
              <Card key={server.id}>
                <CardHeader>
                  <div className="flex items-start justify-between">
                    <div>
                      <CardTitle>
                        <Link to={`/servers/${server.id}`} className="hover:text-emerald-400">
                          {server.name}
                        </Link>
                      </CardTitle>
                      <CardDescription>
                        {getHost(server)}:{getPort(server)}
                      </CardDescription>
                    </div>
                    {actionState[server.id]?.error && (
                      <div className="ml-2 text-xs text-red-400 border border-red-900/40 bg-red-900/20 px-2 py-1 rounded">
                        {actionState[server.id]?.error}
                      </div>
                    )}
                    <StatusBadge status={server.connection_status || 'disconnected'} />
                  </div>
                </CardHeader>
                <CardContent>
                  {isOnline(getStatus(server)) && (
                    <div className="grid grid-cols-2 gap-4 mb-4 text-sm">
                      <div>
                        <p className="text-neutral-400">Uptime</p>
                        <p className="text-white font-medium">
                          {server.status?.uptime ? formatRelativeTime(new Date(Date.now() - server.status.uptime * 1000).toISOString()) : 'N/A'}
                        </p>
                      </div>
                      <div>
                        <p className="text-neutral-400">Players</p>
                        <p className="text-white font-medium">
                          {server.status?.player_count ?? 0} / {server.status?.max_players ?? 0}
                        </p>
                      </div>
                    </div>
                  )}

                  {getLatestMetric(server.id) ? (
                    <div className="grid grid-cols-3 gap-3 mb-4 text-xs">
                      <div>
                        <p className="text-neutral-500">CPU</p>
                        <p className="text-white font-medium">
                          {getLatestMetric(server.id)?.cpu_usage !== undefined
                            ? `${Number(getLatestMetric(server.id)?.cpu_usage).toFixed(1)}%`
                            : 'n/a'}
                        </p>
                      </div>
                      <div>
                        <p className="text-neutral-500">Memory</p>
                        <p className="text-white font-medium">
                          {getLatestMetric(server.id)?.memory_used !== undefined && getLatestMetric(server.id)?.memory_total !== undefined
                            ? `${formatBytes(Number(getLatestMetric(server.id)?.memory_used))} / ${formatBytes(Number(getLatestMetric(server.id)?.memory_total))}`
                            : 'n/a'}
                        </p>
                      </div>
                      <div>
                        <p className="text-neutral-500">Disk</p>
                        <p className="text-white font-medium">
                          {getLatestMetric(server.id)?.disk_used !== undefined && getLatestMetric(server.id)?.disk_total !== undefined
                            ? `${formatBytes(Number(getLatestMetric(server.id)?.disk_used))} / ${formatBytes(Number(getLatestMetric(server.id)?.disk_total))}`
                            : 'n/a'}
                        </p>
                      </div>
                      <div className="col-span-3 text-[11px] text-neutral-500">
                        Updated {formatRelativeTime(getLatestMetric(server.id)?.timestamp ?? new Date().toISOString())}
                      </div>
                    </div>
                  ) : (
                    <div className="text-xs text-neutral-500 mb-4">No metrics yet.</div>
                  )}

                  <div className="flex gap-2">
                    <Button
                      variant="secondary"
                      size="sm"
                      className="flex-1"
                      isLoading={actionState[server.id]?.action === 'start'}
                      disabled={!canStart(getStatus(server))}
                      onClick={() => runAction(server.id, 'start')}
                    >
                      <Play className="w-4 h-4 mr-1" />
                      {actionState[server.id]?.action === 'start' ? 'Starting' : 'Start'}
                    </Button>
                    <Button
                      variant="secondary"
                      size="sm"
                      className="flex-1"
                      isLoading={actionState[server.id]?.action === 'stop'}
                      disabled={!canStop(getStatus(server))}
                      onClick={() => runAction(server.id, 'stop')}
                    >
                      <Square className="w-4 h-4 mr-1" />
                      {actionState[server.id]?.action === 'stop' ? 'Stopping' : 'Stop'}
                    </Button>
                    <Button
                      variant="secondary"
                      size="sm"
                      isLoading={actionState[server.id]?.action === 'restart'}
                      disabled={!canRestart(getStatus(server))}
                      onClick={() => runAction(server.id, 'restart')}
                    >
                      <RotateCw className="w-4 h-4" />
                    </Button>
                  </div>

                  <div className="flex gap-2 mt-3">
                    <Link to={`/console/${server.id}`} className="flex-1">
                      <Button variant="ghost" size="sm" className="w-full">
                        <Terminal className="w-4 h-4 mr-1" />
                        Console
                      </Button>
                    </Link>
                    <Link to={`/backups/${server.id}`} className="flex-1">
                      <Button variant="ghost" size="sm" className="w-full">
                        <Database className="w-4 h-4 mr-1" />
                        Backups
                      </Button>
                    </Link>
                  </div>

                  <div className="mt-3">
                    <Button variant="danger" size="sm" className="w-full" onClick={() => handleDelete(server.id)}>
                      Delete
                    </Button>
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm text-left text-neutral-300">
              <thead className="text-xs uppercase text-neutral-500 border-b border-neutral-800">
                <tr>
                  <th className="px-3 py-3">
                    <input
                      type="checkbox"
                      checked={servers.length > 0 && selectedIds.size === servers.length}
                      onChange={toggleSelectAll}
                    />
                  </th>
                  <th className="px-3 py-3">Server</th>
                  <th className="px-3 py-3">Host</th>
                  <th className="px-3 py-3">Status</th>
                  <th className="px-3 py-3">Metrics</th>
                  <th className="px-3 py-3">Actions</th>
                </tr>
              </thead>
              <tbody>
                {servers.map((server) => (
                  <tr key={server.id} className="border-b border-neutral-900">
                    <td className="px-3 py-3">
                      <input
                        type="checkbox"
                        checked={selectedIds.has(server.id)}
                        onChange={() => toggleSelect(server.id)}
                      />
                    </td>
                    <td className="px-3 py-3">
                      <div className="text-white font-medium">
                        <Link to={`/servers/${server.id}`} className="hover:text-emerald-400">
                          {server.name}
                        </Link>
                      </div>
                      <div className="text-xs text-neutral-500">{server.id}</div>
                      {actionState[server.id]?.error && (
                        <div className="text-xs text-red-400 mt-1">{actionState[server.id]?.error}</div>
                      )}
                    </td>
                    <td className="px-3 py-3">
                      {getHost(server)}:{getPort(server)}
                    </td>
                    <td className="px-3 py-3">
                      <span className={`px-2 py-1 rounded-full text-xs font-medium ${
                        isOnline(getStatus(server))
                          ? 'bg-emerald-900/30 text-emerald-400 border border-emerald-800'
                          : 'bg-neutral-800 text-neutral-400 border border-neutral-700'
                      }`}>
                        {getStatus(server)}
                      </span>
                    </td>
                    <td className="px-3 py-3">
                      {getLatestMetric(server.id) ? (
                        <div className="space-y-1 text-xs">
                          <div className="text-neutral-300">
                            CPU {getLatestMetric(server.id)?.cpu_usage !== undefined ? `${Number(getLatestMetric(server.id)?.cpu_usage).toFixed(1)}%` : 'n/a'}
                          </div>
                          <div className="text-neutral-300">
                            Mem {getLatestMetric(server.id)?.memory_used !== undefined && getLatestMetric(server.id)?.memory_total !== undefined
                              ? `${formatBytes(Number(getLatestMetric(server.id)?.memory_used))} / ${formatBytes(Number(getLatestMetric(server.id)?.memory_total))}`
                              : 'n/a'}
                          </div>
                          <div className="text-neutral-500">
                            {formatRelativeTime(getLatestMetric(server.id)?.timestamp ?? new Date().toISOString())}
                          </div>
                        </div>
                      ) : (
                        <span className="text-xs text-neutral-500">No metrics</span>
                      )}
                    </td>
                    <td className="px-3 py-3">
                      <div className="flex flex-wrap gap-2">
                        <Button
                          variant="secondary"
                          size="sm"
                          isLoading={actionState[server.id]?.action === 'start'}
                          disabled={!canStart(getStatus(server))}
                          onClick={() => runAction(server.id, 'start')}
                        >
                          <Play className="w-4 h-4" />
                        </Button>
                        <Button
                          variant="secondary"
                          size="sm"
                          isLoading={actionState[server.id]?.action === 'stop'}
                          disabled={!canStop(getStatus(server))}
                          onClick={() => runAction(server.id, 'stop')}
                        >
                          <Square className="w-4 h-4" />
                        </Button>
                        <Button
                          variant="secondary"
                          size="sm"
                          isLoading={actionState[server.id]?.action === 'restart'}
                          disabled={!canRestart(getStatus(server))}
                          onClick={() => runAction(server.id, 'restart')}
                        >
                          <RotateCw className="w-4 h-4" />
                        </Button>
                        <Button variant="danger" size="sm" onClick={() => handleDelete(server.id)}>
                          Delete
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      )}
    </div>
  );
}
