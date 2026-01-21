import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient, useQueries } from '@tanstack/react-query';
import { useParams } from 'react-router-dom';
import { backupsApi, serversApi } from '@/api';
import type { BackupSchedule } from '@/api/types';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Input } from '@/components/Input';
import { Download, RefreshCw, Save, Trash2, Play } from 'lucide-react';
import { formatBytes, formatRelativeTime, formatDate } from '@/utils/format';

const defaultSchedule: BackupSchedule = {
  id: '',
  server_id: '',
  enabled: false,
  schedule: '',
  directories: [],
  exclude: [],
  destination: {
    type: 'local',
    path: '',
  },
  retention_count: 7,
  compression: {
    type: 'gzip',
    level: 6,
  },
  run_as_user: '',
  use_sudo: false,
};

const parseLines = (value: string) =>
  value
    .replace(/\\n/g, '\n')
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean);

const cronMarkerPrefix = '# hsm-backup:';

const getCronLinesForSchedule = (
  lines: string[] | undefined,
  serverId: string,
  scheduleId?: string,
) => {
  if (!lines) return [];
  const baseMarker = `${cronMarkerPrefix}${serverId}`;
  if (!scheduleId) {
    return lines.filter((line) => line.includes(baseMarker));
  }

  const scheduleMarker = `${baseMarker}:${scheduleId}`;
  const scheduleMatches = lines.filter((line) => line.includes(scheduleMarker));
  if (scheduleMatches.length > 0) return scheduleMatches;
  return lines.filter((line) => line.includes(baseMarker));
};

export function BackupsPage() {
  const { serverId } = useParams<{ serverId: string }>();
  const queryClient = useQueryClient();

  const { data: servers } = useQuery({
    queryKey: ['servers'],
    queryFn: () => serversApi.listServers(),
  });

  const [selectedServerId, setSelectedServerId] = useState<string>('');
  const [selectedScheduleId, setSelectedScheduleId] = useState<string>('');
  const [isCreatingNewSchedule, setIsCreatingNewSchedule] = useState(false);

  const { data: server } = useQuery({
    queryKey: ['server', serverId],
    queryFn: () => serversApi.getServer(serverId || ''),
    enabled: !!serverId,
  });

  const { data: schedules, isLoading: scheduleLoading } = useQuery({
    queryKey: ['backup-schedules', serverId],
    queryFn: () => backupsApi.listSchedules(serverId || ''),
    enabled: !!serverId,
  });

  const { data: backups, isLoading: backupsLoading } = useQuery({
    queryKey: ['backups', serverId],
    queryFn: () => backupsApi.listBackups(serverId || ''),
    enabled: !!serverId,
  });

  const resolvedServerId = serverId || selectedServerId || servers?.[0]?.id || '';
  const activeSchedulesQuery = useQuery({
    queryKey: ['backup-schedules', resolvedServerId],
    queryFn: () => backupsApi.listSchedules(resolvedServerId),
    enabled: !!resolvedServerId && !serverId,
  });

  const currentSchedules = serverId ? schedules : activeSchedulesQuery.data;
  const selectedSchedule = useMemo(() => {
    if (!currentSchedules || currentSchedules.length === 0) return undefined;
    return currentSchedules.find((item) => item.id === selectedScheduleId) || currentSchedules[0];
  }, [currentSchedules, selectedScheduleId]);

  const schedulesQuery = useQueries({
    queries:
      servers?.map((srv) => ({
        queryKey: ['backup-schedules', srv.id],
        queryFn: () => backupsApi.listSchedules(srv.id),
        enabled: !!servers?.length,
      })) ?? [],
  });

  const scheduleList = useMemo(() => {
    if (!servers) return [];
    return servers.flatMap((srv, index) => {
      const items = schedulesQuery[index]?.data || [];
      return items.map((schedule) => ({ server: srv, schedule }));
    });
  }, [servers, schedulesQuery]);

  const [form, setForm] = useState<BackupSchedule>(defaultSchedule);
  const [directoriesText, setDirectoriesText] = useState('');
  const [excludeText, setExcludeText] = useState('');
  const [cronByServer, setCronByServer] = useState<Record<string, string[]>>({});
  const [saveError, setSaveError] = useState<string>('');
  const [saveSuccess, setSaveSuccess] = useState<string>('');

  const handleConfigureJob = (serverId: string, schedule?: BackupSchedule | null) => {
    setSelectedServerId(serverId);
    setSelectedScheduleId(schedule?.id || '');
    setIsCreatingNewSchedule(!schedule);
    setSaveError('');
    setSaveSuccess('');
    if (schedule) {
      setForm({
        ...defaultSchedule,
        ...schedule,
        server_id: serverId,
        destination: {
          ...defaultSchedule.destination,
          ...schedule.destination,
        },
        compression: {
          ...defaultSchedule.compression,
          ...schedule.compression,
        },
        run_as_user: schedule.run_as_user ?? defaultSchedule.run_as_user,
        use_sudo: schedule.use_sudo ?? defaultSchedule.use_sudo,
      });
      setDirectoriesText((schedule.directories || []).join('\n'));
      setExcludeText((schedule.exclude || []).join('\n'));
    } else {
      const serverDefaults = servers?.find((srv) => srv.id === serverId);
      setForm((prev) => ({
        ...defaultSchedule,
        ...prev,
        server_id: serverId,
        run_as_user: serverDefaults?.dependencies?.service_user || prev.run_as_user || '',
        use_sudo: serverDefaults?.dependencies?.use_sudo ?? prev.use_sudo ?? false,
      }));
      setDirectoriesText('');
      setExcludeText('');
    }
  };

  useEffect(() => {
    const currentServerId = resolvedServerId;
    if (!currentServerId) return;
    if (isCreatingNewSchedule && !selectedScheduleId) {
      return;
    }
    const scheduleListForServer = serverId ? schedules : activeSchedulesQuery.data;
    const currentSchedule = scheduleListForServer?.find((item) => item.id === selectedScheduleId)
      || scheduleListForServer?.[0];
    if (currentSchedule) {
      setForm({
        ...defaultSchedule,
        ...currentSchedule,
        server_id: currentServerId,
        destination: {
          ...defaultSchedule.destination,
          ...currentSchedule.destination,
        },
        compression: {
          ...defaultSchedule.compression,
          ...currentSchedule.compression,
        },
        run_as_user: currentSchedule.run_as_user ?? defaultSchedule.run_as_user,
        use_sudo: currentSchedule.use_sudo ?? defaultSchedule.use_sudo,
      });
      setDirectoriesText((currentSchedule.directories || []).join('\n'));
      setExcludeText((currentSchedule.exclude || []).join('\n'));
      setSelectedScheduleId(currentSchedule.id || '');
      return;
    }

    const serverDefaults = servers?.find((srv) => srv.id === currentServerId);
    setForm((prev) => ({
      ...defaultSchedule,
      ...prev,
      server_id: currentServerId,
      run_as_user: serverDefaults?.dependencies?.service_user || prev.run_as_user || '',
      use_sudo: serverDefaults?.dependencies?.use_sudo ?? prev.use_sudo ?? false,
    }));
    setDirectoriesText('');
    setExcludeText('');
  }, [activeSchedulesQuery.data, resolvedServerId, schedules, serverId, servers, selectedScheduleId, isCreatingNewSchedule]);

  const workingDir = server?.server?.working_directory || '';

  const schedulePayload = useMemo<BackupSchedule>(() => {
    return {
      ...form,
      directories: parseLines(directoriesText),
      exclude: parseLines(excludeText),
    };
  }, [form, directoriesText, excludeText]);

  const saveScheduleMutation = useMutation({
    mutationFn: () => {
      if (selectedScheduleId) {
        return backupsApi.updateScheduleById(serverId || '', selectedScheduleId, schedulePayload);
      }
      return backupsApi.createSchedule(serverId || '', schedulePayload);
    },
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ['backup-schedules', serverId] });
      setSelectedScheduleId(data.id);
      setIsCreatingNewSchedule(false);
      setSaveError('');
      setSaveSuccess('Schedule saved.');
    },
    onError: (error) => {
      setSaveSuccess('');
      setSaveError(error instanceof Error ? error.message : 'Failed to save schedule.');
    },
  });

  const initializeDefaultsMutation = useMutation({
    mutationFn: () => backupsApi.initializeDefaultSchedule(resolvedServerId || serverId || ''),
    onSuccess: (data) => {
      const targetId = resolvedServerId || serverId;
      queryClient.setQueryData(['backup-schedules', targetId], (prev: BackupSchedule[] | undefined) => {
        if (!prev) return [data];
        return prev.some((item) => item.id === data.id) ? prev : [...prev, data];
      });
      queryClient.invalidateQueries({ queryKey: ['backups', targetId] });
    },
  });

  const saveJobMutation = useMutation({
    mutationFn: () => {
      if (selectedScheduleId) {
        return backupsApi.updateScheduleById(resolvedServerId, selectedScheduleId, schedulePayload);
      }
      return backupsApi.createSchedule(resolvedServerId, schedulePayload);
    },
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ['backup-schedules', resolvedServerId] });
      setSelectedScheduleId(data.id);
      setIsCreatingNewSchedule(false);
      setSaveError('');
      setSaveSuccess('Schedule saved.');
    },
    onError: (error) => {
      setSaveSuccess('');
      setSaveError(error instanceof Error ? error.message : 'Failed to save schedule.');
    },
  });

  const runBackupMutation = useMutation({
    mutationFn: () =>
      backupsApi.createBackup(serverId || '', {
        directories: schedulePayload.directories,
        exclude: schedulePayload.exclude,
        working_dir: workingDir,
        destination: schedulePayload.destination,
        compression: schedulePayload.compression,
        run_as_user: schedulePayload.run_as_user,
        use_sudo: schedulePayload.use_sudo,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backups', serverId] });
    },
  });

  const deleteBackupMutation = useMutation({
    mutationFn: (backupId: string) => backupsApi.deleteBackup(serverId || '', backupId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backups', serverId] });
    },
  });

  const restoreBackupMutation = useMutation({
    mutationFn: (backupId: string) =>
      backupsApi.restoreBackup(serverId || '', backupId, { destination: workingDir || '/' }),
  });

  const deleteScheduleMutation = useMutation({
    mutationFn: ({ serverId: targetId, scheduleId }: { serverId: string; scheduleId: string }) =>
      backupsApi.deleteScheduleById(targetId, scheduleId),
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({ queryKey: ['backup-schedules', variables.serverId] });
      queryClient.invalidateQueries({ queryKey: ['backups', variables.serverId] });
      setCronByServer((prev) => {
        const next = { ...prev };
        delete next[variables.serverId];
        return next;
      });
      if (variables.scheduleId === selectedScheduleId) {
        setSelectedScheduleId('');
      }
    },
  });

  const cronQueryMutation = useMutation({
    mutationFn: (targetId: string) => backupsApi.getCron(targetId),
    onSuccess: (data, targetId) => {
      setCronByServer((prev) => ({ ...prev, [targetId]: data.lines }));
    },
  });

  if (!serverId) {
    return (
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-3xl font-bold text-white">Backups</h1>
            <p className="text-neutral-400 mt-1">
              Create and manage backup jobs across your servers.
            </p>
          </div>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>Create Backup Job</CardTitle>
            <CardDescription>
              Select a server and configure its scheduled backup job.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-6">
            {(saveError || saveSuccess) && (
              <div
                className={
                  saveError
                    ? 'rounded-lg border border-red-500/40 bg-red-500/10 p-3 text-sm text-red-200'
                    : 'rounded-lg border border-emerald-500/40 bg-emerald-500/10 p-3 text-sm text-emerald-200'
                }
              >
                {saveError || saveSuccess}
              </div>
            )}
            <div>
              <label className="block text-sm font-medium text-neutral-300 mb-1.5">Server</label>
              <select
                value={resolvedServerId}
                onChange={(event) => setSelectedServerId(event.target.value)}
                className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white"
              >
                {(servers || []).map((srv) => (
                  <option key={srv.id} value={srv.id}>
                    {srv.name} ({srv.id})
                  </option>
                ))}
              </select>
            </div>

            {!activeSchedulesQuery.isLoading && (activeSchedulesQuery.data?.length ?? 0) === 0 && (
              <div className="rounded-lg border border-emerald-500/40 bg-emerald-500/10 p-4 text-sm text-emerald-100">
                <div className="font-semibold">Initialize Default Backup Schedule</div>
                <p className="text-emerald-200 mt-1">
                  No backup job exists for this server. Create the default nightly schedule and
                  install the cron job on the server.
                </p>
                <Button
                  variant="primary"
                  className="mt-3"
                  onClick={() => initializeDefaultsMutation.mutate()}
                  disabled={initializeDefaultsMutation.isPending || !resolvedServerId}
                >
                  Initialize Default Schedule
                </Button>
              </div>
            )}

            <div className="flex items-center gap-3">
              <input
                type="checkbox"
                checked={form.enabled}
                onChange={(event) => setForm((prev) => ({ ...prev, enabled: event.target.checked }))}
                className="h-4 w-4 rounded border-neutral-700 bg-neutral-900 text-emerald-500 focus:ring-emerald-500"
              />
              <span className="text-sm text-neutral-300">Enable scheduled backups</span>
            </div>

            <Input
              label="Cron schedule"
              placeholder="0 3 * * *"
              value={form.schedule}
              onChange={(event) => setForm((prev) => ({ ...prev, schedule: event.target.value }))}
            />

            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <label className="block text-sm font-medium text-neutral-300 mb-1.5">
                  Directories / files to back up
                </label>
                <textarea
                  value={directoriesText}
                  onChange={(event) => setDirectoriesText(event.target.value)}
                  placeholder="world\nconfig\nplugins"
                  rows={6}
                  className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white placeholder-neutral-500 focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:border-transparent"
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-neutral-300 mb-1.5">
                  Exclude patterns (optional)
                </label>
                <textarea
                  value={excludeText}
                  onChange={(event) => setExcludeText(event.target.value)}
                  placeholder="logs\n*.tmp"
                  rows={6}
                  className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white placeholder-neutral-500 focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:border-transparent"
                />
              </div>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              <Input
                label="Retention count"
                type="number"
                min={0}
                value={form.retention_count}
                onChange={(event) =>
                  setForm((prev) => ({ ...prev, retention_count: Number(event.target.value) }))
                }
              />

              <div>
                <label className="block text-sm font-medium text-neutral-300 mb-1.5">
                  Compression type
                </label>
                <select
                  value={form.compression?.type || 'gzip'}
                  onChange={(event) =>
                    setForm((prev) => ({
                      ...prev,
                      compression: { ...prev.compression, type: event.target.value as 'gzip' | 'none' },
                    }))
                  }
                  className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white"
                >
                  <option value="gzip">Gzip</option>
                  <option value="none">None</option>
                </select>
              </div>

              <Input
                label="Compression level"
                type="number"
                min={1}
                max={9}
                value={form.compression?.level || 6}
                disabled={form.compression?.type === 'none'}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    compression: { ...prev.compression, level: Number(event.target.value) },
                  }))
                }
              />
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <label className="block text-sm font-medium text-neutral-300 mb-1.5">
                  Destination type
                </label>
                <select
                  value={form.destination.type}
                  onChange={(event) =>
                    setForm((prev) => ({
                      ...prev,
                      destination: { ...prev.destination, type: event.target.value as 'local' | 'sftp' | 's3' },
                    }))
                  }
                  className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white"
                >
                  <option value="local">Local</option>
                  <option value="sftp">SFTP</option>
                  <option value="s3">S3</option>
                </select>
              </div>
              <Input
                label="Destination path"
                value={form.destination.path ?? ''}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    destination: { ...prev.destination, path: event.target.value },
                  }))
                }
              />
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <Input
                label="Run as user"
                value={form.run_as_user || ''}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    run_as_user: event.target.value,
                  }))
                }
              />
              <div className="flex items-center gap-3 mt-6">
                <input
                  type="checkbox"
                  checked={form.use_sudo || false}
                  onChange={(event) =>
                    setForm((prev) => ({
                      ...prev,
                      use_sudo: event.target.checked,
                    }))
                  }
                  className="h-4 w-4 rounded border-neutral-700 bg-neutral-900 text-emerald-500 focus:ring-emerald-500"
                />
                <span className="text-sm text-neutral-300">Use sudo when running backup</span>
              </div>
            </div>

            <div className="flex items-center justify-end border-t border-neutral-800 pt-4">
              <Button
                variant="primary"
                onClick={() => saveJobMutation.mutate()}
                disabled={saveJobMutation.isPending || !resolvedServerId}
              >
                <Save className="w-4 h-4 mr-2" />
                Save Job
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Backup Jobs</CardTitle>
            <CardDescription>All scheduled jobs you have access to.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            {scheduleList.length === 0 ? (
              <div className="text-neutral-400">No backup jobs configured yet.</div>
            ) : (
              scheduleList.map(({ server, schedule }, index) => (
                <div
                  key={`${server.id}-${schedule.id || index}`}
                  className="flex flex-col md:flex-row md:items-center md:justify-between gap-3 bg-neutral-900/50 border border-neutral-800 rounded-lg p-4"
                >
                  <div>
                    <p className="text-white font-medium">{server.name}</p>
                    <p className="text-sm text-neutral-400">
                      {schedule?.schedule || 'No schedule'} • Retain {schedule?.retention_count || 0}
                    </p>
                    {schedule?.next_run && (
                      <p className="text-xs text-neutral-500">Next: {formatDate(schedule.next_run)}</p>
                    )}
                    {getCronLinesForSchedule(cronByServer[server.id], server.id, schedule.id).length >
                      0 && (
                      <pre className="text-xs text-neutral-400 mt-2 whitespace-pre-wrap">
                        {getCronLinesForSchedule(
                          cronByServer[server.id],
                          server.id,
                          schedule.id,
                        ).join('\n')}
                      </pre>
                    )}
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <Button
                      variant="secondary"
                      onClick={() => handleConfigureJob(server.id, schedule)}
                    >
                      Configure
                    </Button>
                    <Button
                      variant="secondary"
                      onClick={() => cronQueryMutation.mutate(server.id)}
                    >
                      View cron
                    </Button>
                    <Button
                      variant="danger"
                      onClick={() =>
                        deleteScheduleMutation.mutate({
                          serverId: server.id,
                          scheduleId: schedule.id,
                        })
                      }
                    >
                      Delete
                    </Button>
                  </div>
                </div>
              ))
            )}
          </CardContent>
        </Card>
      </div>
    );
  }


  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold text-white">Backups</h1>
          <p className="text-neutral-400 mt-1">
            {server?.name ? `${server.name} backup configuration` : 'Configure backup schedules'}
          </p>
        </div>
        <div className="flex gap-2">
          <Button
            variant="secondary"
            onClick={() => queryClient.invalidateQueries({ queryKey: ['backups', serverId] })}
          >
            <RefreshCw className="w-4 h-4 mr-2" />
            Refresh
          </Button>
          <Button
            variant="primary"
            onClick={() => runBackupMutation.mutate()}
            disabled={
              runBackupMutation.isPending ||
              schedulePayload.directories.length === 0 ||
              !schedulePayload.destination.path
            }
          >
            <Play className="w-4 h-4 mr-2" />
            Run Backup Now
          </Button>
        </div>
      </div>

      {!scheduleLoading && (schedules?.length ?? 0) === 0 && (backups?.length ?? 0) === 0 && (
        <Card>
          <CardHeader>
            <CardTitle>Initialize Default Backup Schedule</CardTitle>
            <CardDescription>
              No backup jobs exist yet. Create the default nightly job for this server.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button
              variant="primary"
              onClick={() => initializeDefaultsMutation.mutate()}
              disabled={initializeDefaultsMutation.isPending}
            >
              <Save className="w-4 h-4 mr-2" />
              Initialize Default Schedule
            </Button>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Schedule & Options</CardTitle>
          <CardDescription>
            Configure recurring backups, retention history, and compression.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          {(saveError || saveSuccess) && (
            <div
              className={
                saveError
                  ? 'rounded-lg border border-red-500/40 bg-red-500/10 p-3 text-sm text-red-200'
                  : 'rounded-lg border border-emerald-500/40 bg-emerald-500/10 p-3 text-sm text-emerald-200'
              }
            >
              {saveError || saveSuccess}
            </div>
          )}
          {serverId && (
            <div className="flex flex-wrap gap-2">
              <Button
                variant="secondary"
                onClick={() => cronQueryMutation.mutate(serverId)}
              >
                View cron
              </Button>
              <Button
                variant="danger"
                onClick={() =>
                  selectedScheduleId
                    ? deleteScheduleMutation.mutate({ serverId, scheduleId: selectedScheduleId })
                    : undefined
                }
                disabled={!selectedScheduleId}
              >
                Delete schedule
              </Button>
              <Button
                variant="secondary"
                onClick={() => {
                  handleConfigureJob(serverId, null);
                }}
              >
                New schedule
              </Button>
              {selectedScheduleId &&
                getCronLinesForSchedule(
                  cronByServer[serverId],
                  serverId,
                  selectedScheduleId,
                ).length > 0 && (
                  <pre className="text-xs text-neutral-400 mt-2 whitespace-pre-wrap w-full">
                    {getCronLinesForSchedule(
                      cronByServer[serverId],
                      serverId,
                      selectedScheduleId,
                    ).join('\n')}
                  </pre>
                )}
            </div>
          )}
          {serverId && (currentSchedules?.length ?? 0) > 0 && (
            <div>
              <label className="block text-sm font-medium text-neutral-300 mb-1.5">
                Editing schedule
              </label>
              <select
                value={selectedScheduleId || selectedSchedule?.id || ''}
                onChange={(event) => {
                  setSelectedScheduleId(event.target.value);
                  setIsCreatingNewSchedule(false);
                }}
                className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white"
              >
                {(currentSchedules || []).map((item, index) => (
                  <option key={item.id} value={item.id}>
                    Job {index + 1} • {item.schedule || 'No schedule'}
                  </option>
                ))}
              </select>
            </div>
          )}
          <div className="flex items-center gap-3">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(event) => setForm((prev) => ({ ...prev, enabled: event.target.checked }))}
              className="h-4 w-4 rounded border-neutral-700 bg-neutral-900 text-emerald-500 focus:ring-emerald-500"
            />
            <span className="text-sm text-neutral-300">Enable scheduled backups</span>
          </div>

          <Input
            label="Cron schedule"
            placeholder="0 3 * * *"
            value={form.schedule}
            onChange={(event) => setForm((prev) => ({ ...prev, schedule: event.target.value }))}
          />

          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-neutral-300 mb-1.5">
                Directories / files to back up
              </label>
              <textarea
                value={directoriesText}
                onChange={(event) => setDirectoriesText(event.target.value)}
                placeholder="world\nconfig\nplugins"
                rows={6}
                className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white placeholder-neutral-500 focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:border-transparent"
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-neutral-300 mb-1.5">
                Exclude patterns (optional)
              </label>
              <textarea
                value={excludeText}
                onChange={(event) => setExcludeText(event.target.value)}
                placeholder="logs\n*.tmp"
                rows={6}
                className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white placeholder-neutral-500 focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:border-transparent"
              />
            </div>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <Input
              label="Retention count"
              type="number"
              min={0}
              value={form.retention_count}
              onChange={(event) =>
                setForm((prev) => ({ ...prev, retention_count: Number(event.target.value) }))
              }
            />

            <div>
              <label className="block text-sm font-medium text-neutral-300 mb-1.5">
                Compression type
              </label>
              <select
                value={form.compression?.type || 'gzip'}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    compression: { ...prev.compression, type: event.target.value as 'gzip' | 'none' },
                  }))
                }
                className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white"
              >
                <option value="gzip">Gzip</option>
                <option value="none">None</option>
              </select>
            </div>

            <Input
              label="Compression level"
              type="number"
              min={1}
              max={9}
              value={form.compression?.level || 6}
              disabled={form.compression?.type === 'none'}
              onChange={(event) =>
                setForm((prev) => ({
                  ...prev,
                  compression: { ...prev.compression, level: Number(event.target.value) },
                }))
              }
            />
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-neutral-300 mb-1.5">
                Destination type
              </label>
              <select
                value={form.destination.type}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    destination: { ...prev.destination, type: event.target.value as 'local' | 'sftp' | 's3' },
                  }))
                }
                className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white"
              >
                <option value="local">Local</option>
                <option value="sftp">SFTP</option>
                <option value="s3">S3</option>
              </select>
            </div>
            <Input
              label="Destination path"
              value={form.destination.path}
              onChange={(event) =>
                setForm((prev) => ({
                  ...prev,
                  destination: { ...prev.destination, path: event.target.value },
                }))
              }
            />
          </div>

          {form.destination.type === 'sftp' && (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <Input
                label="SFTP host"
                value={form.destination.sftp_host || ''}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    destination: { ...prev.destination, sftp_host: event.target.value },
                  }))
                }
              />
              <Input
                label="SFTP port"
                type="number"
                value={form.destination.sftp_port || 22}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    destination: { ...prev.destination, sftp_port: Number(event.target.value) },
                  }))
                }
              />
              <Input
                label="SFTP username"
                value={form.destination.sftp_username || ''}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    destination: { ...prev.destination, sftp_username: event.target.value },
                  }))
                }
              />
              <Input
                label="SFTP password"
                type="password"
                value={form.destination.sftp_password || ''}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    destination: { ...prev.destination, sftp_password: event.target.value },
                  }))
                }
              />
              <Input
                label="SFTP key path"
                value={form.destination.sftp_key_path || ''}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    destination: { ...prev.destination, sftp_key_path: event.target.value },
                  }))
                }
              />
            </div>
          )}

          {form.destination.type === 's3' && (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <Input
                label="S3 bucket"
                value={form.destination.s3_bucket || ''}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    destination: { ...prev.destination, s3_bucket: event.target.value },
                  }))
                }
              />
              <Input
                label="S3 region"
                value={form.destination.s3_region || ''}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    destination: { ...prev.destination, s3_region: event.target.value },
                  }))
                }
              />
              <Input
                label="S3 access key"
                value={form.destination.s3_access_key || ''}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    destination: { ...prev.destination, s3_access_key: event.target.value },
                  }))
                }
              />
              <Input
                label="S3 secret key"
                type="password"
                value={form.destination.s3_secret_key || ''}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    destination: { ...prev.destination, s3_secret_key: event.target.value },
                  }))
                }
              />
              <Input
                label="S3 endpoint"
                value={form.destination.s3_endpoint || ''}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    destination: { ...prev.destination, s3_endpoint: event.target.value },
                  }))
                }
              />
            </div>
          )}

          <div className="flex items-center justify-between border-t border-neutral-800 pt-4">
            <div className="text-sm text-neutral-400">
              {selectedSchedule?.next_run && (
                <span>Next run: {formatDate(selectedSchedule.next_run)}</span>
              )}
            </div>
            <Button
              variant="primary"
              onClick={() => saveScheduleMutation.mutate()}
              disabled={saveScheduleMutation.isPending}
            >
              <Save className="w-4 h-4 mr-2" />
              Save Schedule
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Backup History</CardTitle>
          <CardDescription>Retention history and restore points for this server.</CardDescription>
        </CardHeader>
        <CardContent>
          {scheduleLoading || backupsLoading ? (
            <div className="text-neutral-400">Loading backups…</div>
          ) : backups && backups.length > 0 ? (
            <div className="space-y-3">
              {backups.map((backup) => (
                <div
                  key={backup.id}
                  className="flex flex-col md:flex-row md:items-center md:justify-between gap-3 bg-neutral-900/50 border border-neutral-800 rounded-lg p-4"
                >
                  <div>
                    <p className="text-white font-medium">{backup.filename}</p>
                    <p className="text-sm text-neutral-400">
                      {formatBytes(backup.size_bytes)} • {formatRelativeTime(backup.created_at)}
                    </p>
                    <p className="text-xs text-neutral-500">{backup.status}</p>
                  </div>
                  <div className="flex gap-2">
                    <Button
                      variant="secondary"
                      onClick={() => restoreBackupMutation.mutate(backup.id)}
                    >
                      <Download className="w-4 h-4 mr-2" />
                      Restore
                    </Button>
                    <Button
                      variant="danger"
                      onClick={() => deleteBackupMutation.mutate(backup.id)}
                    >
                      <Trash2 className="w-4 h-4 mr-2" />
                      Delete
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <div className="text-neutral-400">No backups yet. Run a backup to get started.</div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
