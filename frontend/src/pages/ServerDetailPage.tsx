import { useEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from 'react';
import { useParams, Link } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { releasesApi, serversApi } from '@/api';
import type { ActivityLogEntry, AgentState, DependenciesCheckResponse, NodeExporterStatus, Server as ServerType, ServerMetric, ServerStatus } from '@/api/types';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/Card';
import { Input } from '@/components/Input';
import { Button } from '@/components/Button';
import { Terminal, Database, Play, Square, RotateCw, ArrowLeft, Save } from 'lucide-react';
import { formatBytes, formatRelativeTime } from '@/utils/format';
import { StatusBadge } from '@/components/StatusBadge';
import { HealthCheckPanel } from '@/components/HealthCheckPanel';
import { useAuth } from '@/contexts/AuthContext';

export function ServerDetailPage() {
  const { serverId } = useParams();
  const queryClient = useQueryClient();
  const { isAuthenticated, isLoading: authLoading } = useAuth();
  const [actionState, setActionState] = useState<{ action?: 'start' | 'stop' | 'restart' | 'save'; error?: string; success?: string }>({});
  const [testState, setTestState] = useState<{ loading: boolean; result?: Awaited<ReturnType<typeof serversApi.testConnection>>; error?: string }>({ loading: false });
  const [nodeExporterState, setNodeExporterState] = useState<{
    installing: boolean;
    error?: string;
    outputLines: string[];
    currentLine?: string;
    expanded: boolean;
    visible: boolean;
  }>({ installing: false, outputLines: [], expanded: false, visible: false });
  const [depsState, setDepsState] = useState<{
    installing: boolean;
    error?: string;
    outputLines: string[];
    currentLine?: string;
    expanded: boolean;
    visible: boolean;
  }>({ installing: false, outputLines: [], expanded: false, visible: false });
  const [agentInstallState, setAgentInstallState] = useState<{
    installing: boolean;
    error?: string;
    outputLines: string[];
    currentLine?: string;
    expanded: boolean;
    visible: boolean;
  }>({ installing: false, outputLines: [], expanded: false, visible: false });
  const [depsCheck, setDepsCheck] = useState<{ loading: boolean; result?: DependenciesCheckResponse; error?: string }>({ loading: false });
  const [deployState, setDeployState] = useState<{
    deploying: boolean;
    error?: string;
    outputLines: string[];
    currentLine?: string;
    expanded: boolean;
    visible: boolean;
  }>({ deploying: false, outputLines: [], expanded: false, visible: false });
  const [runtimeOptions, setRuntimeOptions] = useState({
    install_dir: '',
    service_user: '',
    use_sudo: true,
    java_xms: '10G',
    java_xmx: '10G',
    java_metaspace: '2560M',
    enable_string_dedup: true,
    enable_aot: true,
    enable_backup: true,
    backup_dir: '',
    backup_frequency: '30m',
    assets_path: '',
    extra_java_args: '',
    extra_server_args: '',
    show_advanced: false,
  });
  const [benchmarkState, setBenchmarkState] = useState<{
    running: boolean;
    error?: string;
    outputLines: string[];
    currentLine?: string;
    expanded: boolean;
    visible: boolean;
  }>({ running: false, outputLines: [], expanded: false, visible: false });
  const [deployOptions, setDeployOptions] = useState({
    package_name: '',
    install_dir: '',
    service_user: '',
    use_sudo: true,
    java_xms: '10G',
    java_xmx: '10G',
    java_metaspace: '2560M',
    enable_string_dedup: true,
    enable_aot: true,
    enable_backup: true,
    backup_dir: '',
    backup_frequency: 30,
    assets_path: '',
    extra_java_args: '',
    extra_server_args: '',
    show_advanced: false,
  });
  const [benchmarkOptions, setBenchmarkOptions] = useState({
    size_mb: 64,
    block_mb: 4,
    remove_after: true,
  });
  const deployAbortRef = useRef<AbortController | null>(null);
  const benchmarkAbortRef = useRef<AbortController | null>(null);
  const serverStreamSocketRef = useRef<WebSocket | null>(null);
  const [depsOptions, setDepsOptions] = useState({
    skip_update: false,
    use_sudo: true,
    create_user: true,
    service_user: 'hytale',
    service_groups: '',
    install_dir: '~/hytale-server',
    save_config: true,
    show_advanced: false,
  });
  const depsAbortRef = useRef<AbortController | null>(null);
  const agentAbortRef = useRef<AbortController | null>(null);
  const [agentOptions, setAgentOptions] = useState({
    use_sudo: true,
  });
  const [killState, setKillState] = useState<{ loading: boolean; pid?: number; error?: string; success?: string }>({ loading: false });
  const [detectState, setDetectState] = useState<{ loading: boolean; error?: string }>({ loading: false });
  const installAbortRef = useRef<AbortController | null>(null);

  const appendStreamLines = <T extends { outputLines: string[]; currentLine?: string }>(
    setter: Dispatch<SetStateAction<T>>,
    newLines: string[],
  ) => {
    if (newLines.length === 0) {
      return;
    }
    setter((prev) => {
      const merged = prev.outputLines.concat(newLines);
      const capped = merged.length > 500 ? merged.slice(merged.length - 500) : merged;
      return {
        ...prev,
        outputLines: capped,
        currentLine: newLines[newLines.length - 1],
      };
    });
  };

  const appendBenchmarkLines = (newLines: string[]) => {
    if (newLines.length === 0) {
      return;
    }
    setBenchmarkState((prev) => {
      let lines = [...prev.outputLines];
      let shouldStop = false;
      for (const line of newLines) {
        if (!line) {
          continue;
        }
        if (line.startsWith('Progress:') && lines.length > 0 && lines[lines.length - 1].startsWith('Progress:')) {
          lines[lines.length - 1] = line;
          continue;
        }
        if (line.startsWith('Benchmark complete') && lines.length > 0 && lines[lines.length - 1].startsWith('Progress:')) {
          lines[lines.length - 1] = line;
          shouldStop = true;
          continue;
        }
        if (line.startsWith('Benchmark complete') || line.startsWith('Benchmark failed')) {
          shouldStop = true;
        }
        lines.push(line);
      }

      if (lines.length > 200) {
        lines = lines.slice(lines.length - 200);
      }

      const lastLine = newLines[newLines.length - 1];
      return {
        ...prev,
        outputLines: lines,
        currentLine: lastLine || prev.currentLine,
        running: shouldStop ? false : prev.running,
      };
    });
  };

  const buildWsUrl = (path: string) => {
    const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws';
    const host = window.location.hostname;
    const port = window.location.port;
    const backendPort = port === '5173' ? '8080' : port;
    const hostWithPort = backendPort ? `${host}:${backendPort}` : host;
    return `${protocol}://${hostWithPort}${path}`;
  };

  const applyTaskSnapshot = (task: {
    id: string;
    task: string;
    status: 'running' | 'complete' | 'failed';
    last_line?: string;
    error?: string;
  }) => {
    if (task.task === 'transfer-benchmark') {
      setBenchmarkState((prev) => ({
        ...prev,
        running: task.status === 'running',
        visible: true,
        error: task.status === 'failed' ? task.error || prev.error : prev.error,
        currentLine: task.last_line || prev.currentLine,
      }));
      return;
    }
    if (task.task === 'dependencies-install') {
      setDepsState((prev) => ({
        ...prev,
        installing: task.status === 'running',
        visible: true,
        error: task.status === 'failed' ? task.error || prev.error : prev.error,
        currentLine: task.last_line || prev.currentLine,
      }));
      return;
    }
    if (task.task === 'agent-install') {
      setAgentInstallState((prev) => ({
        ...prev,
        installing: task.status === 'running',
        visible: true,
        error: task.status === 'failed' ? task.error || prev.error : prev.error,
        currentLine: task.last_line || prev.currentLine,
      }));
      return;
    }
    if (task.task === 'release-deploy') {
      setDeployState((prev) => ({
        ...prev,
        deploying: task.status === 'running',
        visible: true,
        error: task.status === 'failed' ? task.error || prev.error : prev.error,
        currentLine: task.last_line || prev.currentLine,
      }));
      return;
    }
    if (task.task === 'node-exporter-install') {
      setNodeExporterState((prev) => ({
        ...prev,
        installing: task.status === 'running',
        visible: true,
        error: task.status === 'failed' ? task.error || prev.error : prev.error,
        currentLine: task.last_line || prev.currentLine,
      }));
    }
  };

  const ensureServerStreamSocket = () => {
    if (!serverId) {
      return;
    }
    const existing = serverStreamSocketRef.current;
    if (existing && (existing.readyState === WebSocket.OPEN || existing.readyState === WebSocket.CONNECTING)) {
      return;
    }

    if (!isAuthenticated && !authLoading) {
      setBenchmarkState((prev) =>
        prev.running
          ? {
              ...prev,
              running: false,
              error: 'Authentication required to stream benchmark output.',
            }
          : prev,
      );
      setDepsState((prev) =>
        prev.installing
          ? {
              ...prev,
              installing: false,
              error: 'Authentication required to stream dependency output.',
            }
          : prev,
      );
      setAgentInstallState((prev) =>
        prev.installing
          ? {
              ...prev,
              installing: false,
              error: 'Authentication required to stream agent install output.',
            }
          : prev,
      );
      setDeployState((prev) =>
        prev.deploying
          ? {
              ...prev,
              deploying: false,
              error: 'Authentication required to stream deploy output.',
            }
          : prev,
      );
      setNodeExporterState((prev) =>
        prev.installing
          ? {
              ...prev,
              installing: false,
              error: 'Authentication required to stream install output.',
            }
          : prev,
      );
      return;
    }

    const wsUrl = buildWsUrl(`/api/v1/ws/servers/${serverId}/tasks`);
    const socket = new WebSocket(wsUrl);
    serverStreamSocketRef.current = socket;

    const handleTaskOutput = (task: string, line: string) => {
      if (!line) {
        return;
      }
      if (task === 'transfer-benchmark') {
        appendBenchmarkLines([line]);
        return;
      }
      if (task === 'dependencies-install') {
        appendStreamLines(setDepsState, [line]);
        if (line.startsWith('Install failed:')) {
          setDepsState((prev) => ({
            ...prev,
            installing: false,
            error: line,
          }));
        }
        if (line.startsWith('Dependencies install complete.')) {
          setDepsState((prev) => ({
            ...prev,
            installing: false,
          }));
        }
        return;
      }
      if (task === 'agent-install') {
        appendStreamLines(setAgentInstallState, [line]);
        if (line.startsWith('Install failed:')) {
          setAgentInstallState((prev) => ({
            ...prev,
            installing: false,
            error: line,
          }));
        }
        if (line.startsWith('Agent install complete.')) {
          setAgentInstallState((prev) => ({
            ...prev,
            installing: false,
          }));
        }
        return;
      }
      if (task === 'release-deploy') {
        appendStreamLines(setDeployState, [line]);
        const deployFailurePrefixes = [
          'Deploy failed:',
          'Release not found:',
          'Release file missing:',
          'Failed to load releases:',
          'Failed to resolve user home:',
          'Upload failed:',
        ];
        if (deployFailurePrefixes.some((prefix) => line.startsWith(prefix))) {
          setDeployState((prev) => ({
            ...prev,
            deploying: false,
            error: line,
          }));
        }
        if (line.startsWith('Release deployment complete.')) {
          setDeployState((prev) => ({
            ...prev,
            deploying: false,
          }));
        }
        return;
      }
      if (task === 'node-exporter-install') {
        appendStreamLines(setNodeExporterState, [line]);
        if (line.startsWith('Install failed:')) {
          setNodeExporterState((prev) => ({
            ...prev,
            installing: false,
            error: line,
          }));
        }
        if (line.startsWith('Node exporter install complete.')) {
          setNodeExporterState((prev) => ({
            ...prev,
            installing: false,
          }));
          void refetchNodeExporterStatus();
          void queryClient.invalidateQueries({ queryKey: ['node-exporter-status', serverId] });
        }
      }
    };

    socket.onmessage = (event) => {
      const raw = typeof event.data === 'string' ? event.data : '';
      if (!raw) {
        return;
      }
      const messages = raw.split('\n').filter(Boolean);
      for (const message of messages) {
        try {
          const parsed = JSON.parse(message) as { type?: string; payload?: any };
          if (parsed.type === 'task_output') {
            if (typeof parsed.payload?.task !== 'string' || typeof parsed.payload?.line !== 'string') {
              continue;
            }
            handleTaskOutput(parsed.payload.task, parsed.payload.line);
            continue;
          }
          if (parsed.type === 'task_status') {
            if (typeof parsed.payload?.task !== 'string' || typeof parsed.payload?.status !== 'string' || typeof parsed.payload?.task_id !== 'string') {
              continue;
            }
            applyTaskSnapshot({
              id: parsed.payload.task_id,
              task: parsed.payload.task,
              status: parsed.payload.status,
              last_line: parsed.payload.last_line,
              error: parsed.payload.error,
            });
          }
        } catch {
          // ignore malformed messages
        }
      }
    };

    socket.onclose = () => {
      if (serverStreamSocketRef.current === socket) {
        serverStreamSocketRef.current = null;
      }
    };

    socket.onerror = () => {
      setBenchmarkState((prev) =>
        prev.running
          ? {
              ...prev,
              error: 'Benchmark stream disconnected. Re-run to reconnect.',
            }
          : prev,
      );
      setDepsState((prev) =>
        prev.installing
          ? {
              ...prev,
              error: 'Dependency stream disconnected. Re-run to reconnect.',
            }
          : prev,
      );
      setAgentInstallState((prev) =>
        prev.installing
          ? {
              ...prev,
              error: 'Agent install stream disconnected. Re-run to reconnect.',
            }
          : prev,
      );
      setDeployState((prev) =>
        prev.deploying
          ? {
              ...prev,
              error: 'Deploy stream disconnected. Re-run to reconnect.',
            }
          : prev,
      );
      setNodeExporterState((prev) =>
        prev.installing
          ? {
              ...prev,
              error: 'Install stream disconnected. Re-run to reconnect.',
            }
          : prev,
      );
    };
  };

  useEffect(() => {
    return () => {
      serverStreamSocketRef.current?.close();
      serverStreamSocketRef.current = null;
    };
  }, [serverId]);

  const { data: server, isLoading, error } = useQuery<ServerType>({
    queryKey: ['server', serverId],
    queryFn: () => serversApi.getServer(serverId || ''),
    enabled: Boolean(serverId),
  });

  const { data: releases } = useQuery({
    queryKey: ['releases', { includeRemoved: false }],
    queryFn: () => releasesApi.listReleases(false),
  });

  useEffect(() => {
    if (!server?.dependencies?.configured) {
      return;
    }
    setDepsOptions((prev) => ({
      ...prev,
      skip_update: server.dependencies?.skip_update ?? prev.skip_update,
      use_sudo: server.dependencies?.use_sudo ?? prev.use_sudo,
      create_user: server.dependencies?.create_user ?? prev.create_user,
      service_user: server.dependencies?.service_user ?? prev.service_user,
      service_groups: (server.dependencies?.service_groups || []).join(',') || prev.service_groups,
      install_dir: server.dependencies?.install_dir ?? prev.install_dir,
      save_config: true,
    }));
  }, [server?.dependencies]);

  useEffect(() => {
    if (!server?.dependencies) {
      return;
    }
    setDeployOptions((prev) => ({
      ...prev,
      install_dir: prev.install_dir || server.dependencies?.install_dir || prev.install_dir,
      service_user: prev.service_user || server.dependencies?.service_user || prev.service_user,
      use_sudo: server.dependencies?.use_sudo ?? prev.use_sudo,
      backup_dir: prev.backup_dir || (server.dependencies?.install_dir ? `${server.dependencies.install_dir}/Backups` : prev.backup_dir),
      assets_path: prev.assets_path || (server.dependencies?.install_dir ? `${server.dependencies.install_dir}/Assets.zip` : prev.assets_path),
    }));
    setRuntimeOptions((prev) => ({
      ...prev,
      install_dir: prev.install_dir || server.dependencies?.install_dir || prev.install_dir,
      service_user: prev.service_user || server.dependencies?.service_user || prev.service_user,
      use_sudo: server.dependencies?.use_sudo ?? prev.use_sudo,
      backup_dir: prev.backup_dir || (server.dependencies?.install_dir ? `${server.dependencies.install_dir}/Backups` : prev.backup_dir),
      assets_path: prev.assets_path || (server.dependencies?.install_dir ? `${server.dependencies.install_dir}/Assets.zip` : prev.assets_path),
    }));
  }, [server?.dependencies]);

  useEffect(() => {
    if (!releases || releases.length === 0) {
      return;
    }
    setDeployOptions((prev) => {
      if (prev.package_name) {
        return prev;
      }
      const latest = releases[0];
      const name = latest.file_path.split(/[\\/]/).pop() || latest.version;
      const packageName = name.replace(/\.zip$/i, '');
      return { ...prev, package_name: packageName };
    });
  }, [releases]);

  // Load runtime options from server config when server data is available
  useEffect(() => {
    if (!server) return;

    const installDir = server.dependencies?.install_dir;

    setRuntimeOptions((prev) => ({
      ...prev,
      install_dir: server.dependencies?.install_dir || '',
      service_user: server.dependencies?.service_user || '',
      use_sudo: server.dependencies?.use_sudo ?? true,
      java_xms: server.runtime?.java_xms || '',
      java_xmx: server.runtime?.java_xmx || '',
      java_metaspace: server.runtime?.java_metaspace || '',
      enable_string_dedup: server.runtime?.enable_string_dedup ?? false,
      enable_aot: server.runtime?.enable_aot ?? false,
      enable_backup: server.runtime?.enable_backup ?? false,
      backup_dir: server.runtime?.backup_dir ?? (installDir ? `${installDir}/Backups` : ''),
      backup_frequency: server.runtime?.backup_frequency ?? '30m',
      assets_path: server.runtime?.assets_path ?? (installDir ? `${installDir}/Assets.zip` : ''),
      extra_java_args: server.runtime?.extra_java_args || '',
      extra_server_args: server.runtime?.extra_server_args || '',
    }));
  }, [server]);

  const { data: status } = useQuery<ServerStatus>({
    queryKey: ['server-status', serverId],
    queryFn: () => serversApi.getServerStatus(serverId || ''),
    enabled: Boolean(serverId),
    refetchInterval: 10000, // Refresh every 10 seconds
  });

  const { data: metricsHistory } = useQuery<ServerMetric[]>({
    queryKey: ['server-metrics', serverId],
    queryFn: () => serversApi.getMetricsHistory(serverId || '', 25),
    enabled: Boolean(serverId),
  });

  const {
    data: liveMetricsMap,
    error: liveMetricsError,
    isFetching: liveMetricsLoading,
    refetch: refetchLiveMetrics,
  } = useQuery<Record<string, ServerMetric>>({
    queryKey: ['servers-live-metrics'],
    queryFn: serversApi.getLiveMetrics,
    enabled: Boolean(serverId),
    refetchInterval: 15000, // Refresh every 15 seconds
  });

  const liveMetric = serverId ? liveMetricsMap?.[serverId] : undefined;

  const {
    data: nodeExporterStatus,
    error: nodeExporterError,
    refetch: refetchNodeExporterStatus,
    isFetching: nodeExporterLoading,
  } = useQuery<NodeExporterStatus>({
    queryKey: ['node-exporter-status', serverId],
    queryFn: () => serversApi.getNodeExporterStatus(serverId || ''),
    enabled: Boolean(serverId),
    retry: false,
  });

  const { data: activityLog, isFetching: activityLoading } = useQuery<ActivityLogEntry[]>({
    queryKey: ['server-activity', serverId],
    queryFn: () => serversApi.getServerActivity(serverId || '', 25),
    enabled: Boolean(serverId),
  });

  const {
    data: agentLiveState,
    error: agentStateError,
    isFetching: agentStateLoading,
    refetch: refetchAgentState,
  } = useQuery<AgentState>({
    queryKey: ['agent-state', serverId],
    queryFn: () => serversApi.getAgentState(serverId || ''),
    enabled: Boolean(serverId),
    refetchInterval: 10000, // Refresh every 10 seconds
  });

  const isOnline = (value?: string) => value === 'running' || value === 'online';
  const canStart = (value?: string) => !isOnline(value);
  const canStop = (value?: string) => isOnline(value) || value === 'starting';
  const canRestart = (value?: string) => isOnline(value) || value === 'starting';

  const host = useMemo(() => server?.connection?.host ?? server?.host ?? 'unknown', [server]);
  const port = useMemo(() => server?.connection?.port ?? server?.port ?? 0, [server]);
  const username = useMemo(() => server?.connection?.username ?? server?.ssh_user ?? 'unknown', [server]);

  const getErrorMessage = (err: unknown, fallback: string) => {
    const maybe = err as { response?: { data?: { error?: string; details?: string } } };
    const error = maybe?.response?.data?.error;
    const details = maybe?.response?.data?.details;
    if (error && details) {
      return `${error}: ${details}`;
    }
    return error ?? details ?? fallback;
  };

  const getAgentErrorDetails = (err: unknown) => {
    const maybe = err as { response?: { data?: Record<string, any> } };
    const data = maybe?.response?.data;
    if (!data) {
      return undefined;
    }
    const { agent_status, listening, journal, process } = data as { agent_status?: string; listening?: string; journal?: string; process?: string };
    if (!agent_status && !listening && !journal && !process) {
      return undefined;
    }
    return { agent_status, listening, journal, process };
  };

  const runAction = async (action: 'start' | 'stop' | 'restart') => {
    if (!serverId) {
      return;
    }

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

    setActionState({ action });

    const runtimePayload = {
      install_dir: runtimeOptions.install_dir || undefined,
      service_user: runtimeOptions.service_user || undefined,
      use_sudo: runtimeOptions.use_sudo,
      java_xms: runtimeOptions.java_xms,
      java_xmx: runtimeOptions.java_xmx,
      java_metaspace: runtimeOptions.java_metaspace,
      enable_string_dedup: runtimeOptions.enable_string_dedup,
      enable_aot: runtimeOptions.enable_aot,
      enable_backup: runtimeOptions.enable_backup,
      backup_dir: runtimeOptions.backup_dir || undefined,
      backup_frequency: runtimeOptions.backup_frequency,
      assets_path: runtimeOptions.assets_path || undefined,
      extra_java_args: runtimeOptions.extra_java_args || undefined,
      extra_server_args: runtimeOptions.extra_server_args || undefined,
    };

    try {
      // Save runtime options to server config before starting/restarting
      if (action === 'start' || action === 'restart') {
        await saveRuntimeOptions();
      }

      console.log(`[RunAction] Executing action: ${action} for server ${serverId}`);

      if (action === 'start') {
        await serversApi.startServer(serverId, runtimePayload);
      } else if (action === 'stop') {
        console.log(`[RunAction] Calling stopServer for ${serverId}`);
        await serversApi.stopServer(serverId);
        console.log(`[RunAction] stopServer completed for ${serverId}`);
      } else {
        await serversApi.restartServer(serverId, runtimePayload);
      }
      await queryClient.invalidateQueries({ queryKey: ['server-status', serverId] });
    } catch (err: unknown) {
      console.error(`[RunAction] Error during ${action}:`, err);
      setActionState({ action: undefined, error: getErrorMessage(err, 'Action failed.') });
      return;
    }

    console.log(`[RunAction] Action ${action} completed successfully`);
    setActionState({ action: undefined, error: undefined });
  };

  const saveRuntimeOptions = async () => {
    if (!serverId || !server) {
      return;
    }

    setActionState({ action: 'save' });

    try {
      // Send complete server definition with updated dependencies and runtime config
      const updatePayload: Partial<ServerType> = {
        id: server.id,
        name: server.name,
        description: server.description,
        connection: server.connection,
        server: server.server,
        backups: server.backups,
        monitoring: server.monitoring,
        dependencies: {
          ...server.dependencies,
          install_dir: runtimeOptions.install_dir || server.dependencies?.install_dir,
          service_user: runtimeOptions.service_user || server.dependencies?.service_user,
          use_sudo: runtimeOptions.use_sudo,
        },
        runtime: {
          java_xms: runtimeOptions.java_xms,
          java_xmx: runtimeOptions.java_xmx,
          java_metaspace: runtimeOptions.java_metaspace,
          enable_string_dedup: runtimeOptions.enable_string_dedup,
          enable_aot: runtimeOptions.enable_aot,
          enable_backup: runtimeOptions.enable_backup,
          backup_dir: runtimeOptions.backup_dir,
          backup_frequency: runtimeOptions.backup_frequency,
          assets_path: runtimeOptions.assets_path,
          extra_java_args: runtimeOptions.extra_java_args,
          extra_server_args: runtimeOptions.extra_server_args,
        },
      };

      console.log('[SaveRuntimeOptions] Sending update payload:', updatePayload);

      await serversApi.updateServer(serverId, updatePayload);
      await queryClient.invalidateQueries({ queryKey: ['server', serverId] });
      setActionState({ action: undefined, success: 'Runtime options saved successfully' });
      
      console.log('[SaveRuntimeOptions] Save successful');

      // Clear success message after 3 seconds
      setTimeout(() => {
        setActionState((prev) => ({ ...prev, success: undefined }));
      }, 3000);
    } catch (err: unknown) {
      console.error('[SaveRuntimeOptions] Save failed:', err);
      setActionState({ action: undefined, error: getErrorMessage(err, 'Failed to save runtime options.') });
    }
  };

  const resetRuntimeOptionsToDefaults = () => {
    const confirmed = window.confirm('Reset runtime options to default values? This will not affect the saved configuration until you click Save.');
    if (!confirmed) return;

    setRuntimeOptions({
      install_dir: '',
      service_user: '',
      use_sudo: true,
      java_xms: '10G',
      java_xmx: '10G',
      java_metaspace: '2560M',
      enable_string_dedup: true,
      enable_aot: true,
      enable_backup: true,
      backup_dir: '',
      backup_frequency: '30m',
      assets_path: '',
      extra_java_args: '',
      extra_server_args: '',
      show_advanced: false,
    });
  };

  const testConnection = async () => {
    if (!serverId) {
      return;
    }

    setTestState({ loading: true });
    try {
      const result = await serversApi.testConnection(serverId);
      setTestState({ loading: false, result });
    } catch (err: unknown) {
      setTestState({ loading: false, error: getErrorMessage(err, 'Connection test failed.') });
    }
  };

  const installNodeExporter = async () => {
    if (!serverId) {
      return;
    }

    const confirmed = window.confirm('Install node_exporter on this server?');
    if (!confirmed) {
      return;
    }

    setNodeExporterState({
      installing: true,
      error: undefined,
      outputLines: [],
      currentLine: 'Starting install...',
      expanded: false,
      visible: true,
    });
    try {
      ensureServerStreamSocket();
      const controller = new AbortController();
      installAbortRef.current = controller;
      const response = await fetch(`/api/v1/servers/${serverId}/node-exporter/install`, {
        method: 'POST',
        credentials: 'include',
        signal: controller.signal,
      });

      if (!response.ok) {
        const errorText = await response.text();
        setNodeExporterState((prev) => ({
          ...prev,
          installing: false,
          error: errorText || 'Install failed.',
        }));
        return;
      }

      installAbortRef.current = null;
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        setNodeExporterState((prev) => ({
          ...prev,
          installing: false,
          error: 'Install cancelled locally. The server may still be installing.',
        }));
        installAbortRef.current = null;
        return;
      }
      setNodeExporterState((prev) => ({
        ...prev,
        installing: false,
        error: getErrorMessage(err, 'Install failed.'),
      }));
      installAbortRef.current = null;
    }
  };

  const installDependencies = async () => {
    if (!serverId) {
      return;
    }

    const confirmed = window.confirm('Install Hytale server dependencies on this server?');
    if (!confirmed) {
      return;
    }

    setDepsState({
      installing: true,
      error: undefined,
      outputLines: [],
      currentLine: 'Starting dependency install...',
      expanded: false,
      visible: true,
    });

    try {
      ensureServerStreamSocket();
      const controller = new AbortController();
      depsAbortRef.current = controller;

      const payload = {
        skip_update: depsOptions.skip_update,
        use_sudo: depsOptions.use_sudo,
        create_user: depsOptions.create_user,
        service_user: depsOptions.service_user,
        service_groups: depsOptions.service_groups
          .split(',')
          .map((value) => value.trim())
          .filter(Boolean),
        install_dir: depsOptions.install_dir,
        save_config: depsOptions.save_config,
      };

      const response = await fetch(`/api/v1/servers/${serverId}/dependencies/install`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(payload),
        credentials: 'include',
        signal: controller.signal,
      });

      if (!response.ok) {
        const errorText = await response.text();
        setDepsState((prev) => ({
          ...prev,
          installing: false,
          error: errorText || 'Install failed.',
        }));
        return;
      }
      depsAbortRef.current = null;
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        setDepsState((prev) => ({
          ...prev,
          installing: false,
          error: 'Install cancelled locally. The server may still be installing.',
        }));
        return;
      }
      setDepsState((prev) => ({
        ...prev,
        installing: false,
        error: getErrorMessage(err, 'Install failed.'),
      }));
    }
  };

  const installAgent = async () => {
    if (!serverId) {
      return;
    }

    const confirmed = window.confirm('Install the monitoring agent on this server?');
    if (!confirmed) {
      return;
    }

    setAgentInstallState({
      installing: true,
      error: undefined,
      outputLines: [],
      currentLine: 'Starting agent install...',
      expanded: false,
      visible: true,
    });

    try {
      ensureServerStreamSocket();
      const controller = new AbortController();
      agentAbortRef.current = controller;

      const payload = {
        use_sudo: agentOptions.use_sudo,
      };

      const response = await fetch(`/api/v1/servers/${serverId}/agent/install`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(payload),
        credentials: 'include',
        signal: controller.signal,
      });

      if (!response.ok) {
        const errorText = await response.text();
        setAgentInstallState((prev) => ({
          ...prev,
          installing: false,
          error: errorText || 'Install failed.',
        }));
        return;
      }
      agentAbortRef.current = null;
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        setAgentInstallState((prev) => ({
          ...prev,
          installing: false,
          error: 'Install cancelled locally. The server may still be installing.',
        }));
        return;
      }
      setAgentInstallState((prev) => ({
        ...prev,
        installing: false,
        error: getErrorMessage(err, 'Install failed.'),
      }));
    }
  };

  const killProcess = async (pid: number) => {
    if (!serverId) {
      return;
    }
    const confirmed = window.confirm(`Kill process ${pid}?`);
    if (!confirmed) {
      return;
    }
    setKillState({ loading: true, pid, error: undefined, success: undefined });
    try {
      await serversApi.killProcess(serverId, { pid });
      setKillState({ loading: false, pid: undefined, error: undefined, success: `Process ${pid} killed.` });
      await queryClient.invalidateQueries({ queryKey: ['agent-state', serverId] });
    } catch (err: unknown) {
      setKillState({ loading: false, pid, error: getErrorMessage(err, 'Kill failed.'), success: undefined });
    }
  };

  const checkDependencies = async () => {
    if (!serverId) {
      return;
    }
    setDepsCheck({ loading: true });
    try {
      const result = await serversApi.checkDependencies(serverId);
      setDepsCheck({ loading: false, result });
    } catch (err: unknown) {
      setDepsCheck({ loading: false, error: getErrorMessage(err, 'Check failed.') });
    }
  };

  const cancelDependenciesInstall = () => {
    depsAbortRef.current?.abort();
  };

  const cancelAgentInstall = () => {
    agentAbortRef.current?.abort();
  };

  const deployRelease = async () => {
    if (!serverId) {
      return;
    }
    if (!deployOptions.package_name) {
      setDeployState((prev) => ({ ...prev, error: 'Select a release package to deploy.' }));
      return;
    }

    setDeployState({
      deploying: true,
      error: undefined,
      outputLines: [],
      currentLine: 'Starting deployment...',
      expanded: false,
      visible: true,
    });

    try {
      ensureServerStreamSocket();
      const controller = new AbortController();
      deployAbortRef.current = controller;

      const payload = {
        package_name: deployOptions.package_name,
        install_dir: deployOptions.install_dir || undefined,
        service_user: deployOptions.service_user || undefined,
        use_sudo: deployOptions.use_sudo,
        java_xms: deployOptions.java_xms,
        java_xmx: deployOptions.java_xmx,
        java_metaspace: deployOptions.java_metaspace,
        enable_string_dedup: deployOptions.enable_string_dedup,
        enable_aot: deployOptions.enable_aot,
        enable_backup: deployOptions.enable_backup,
        backup_dir: deployOptions.backup_dir || undefined,
        backup_frequency: deployOptions.backup_frequency,
        assets_path: deployOptions.assets_path || undefined,
        extra_java_args: deployOptions.extra_java_args || undefined,
        extra_server_args: deployOptions.extra_server_args || undefined,
      };

      const response = await fetch(`/api/v1/servers/${serverId}/releases/deploy`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(payload),
        credentials: 'include',
        signal: controller.signal,
      });

      if (!response.ok) {
        const errorText = await response.text();
        setDeployState((prev) => ({
          ...prev,
          deploying: false,
          error: errorText || 'Deploy failed.',
        }));
        return;
      }
      deployAbortRef.current = null;
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        setDeployState((prev) => ({
          ...prev,
          deploying: false,
          error: 'Deploy cancelled locally. The server may still be deploying.',
        }));
        return;
      }
      setDeployState((prev) => ({
        ...prev,
        deploying: false,
        error: getErrorMessage(err, 'Deploy failed.'),
      }));
    }
  };

  const cancelDeploy = () => {
    deployAbortRef.current?.abort();
  };

  const runTransferBenchmark = async () => {
    if (!serverId) {
      return;
    }

    setBenchmarkState({
      running: true,
      error: undefined,
      outputLines: [],
      currentLine: 'Starting benchmark...',
      expanded: false,
      visible: true,
    });

    try {
      ensureServerStreamSocket();
      const controller = new AbortController();
      benchmarkAbortRef.current = controller;

      const payload = {
        size_mb: Number(benchmarkOptions.size_mb),
        block_mb: Number(benchmarkOptions.block_mb),
        remove_after: benchmarkOptions.remove_after,
      };

      const response = await fetch(`/api/v1/servers/${serverId}/transfer/benchmark`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(payload),
        credentials: 'include',
        signal: controller.signal,
      });

      if (!response.ok) {
        const errorText = await response.text();
        setBenchmarkState((prev) => ({
          ...prev,
          running: false,
          error: errorText || 'Benchmark failed to start.',
        }));
        return;
      }
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        setBenchmarkState((prev) => ({
          ...prev,
          running: false,
          error: 'Benchmark cancelled locally. The server may still be running.',
        }));
        return;
      }
      setBenchmarkState((prev) => ({
        ...prev,
        running: false,
        error: getErrorMessage(err, 'Benchmark failed.'),
      }));
    }
  };

  const cancelBenchmark = () => {
    benchmarkAbortRef.current?.abort();
  };

  const cancelInstall = () => {
    if (installAbortRef.current) {
      installAbortRef.current.abort();
    }
  };

  const detectNodeExporter = async () => {
    if (!serverId) {
      return;
    }

    setDetectState({ loading: true });
    try {
      const result = await refetchNodeExporterStatus();
      if (result.error) {
        setDetectState({ loading: false, error: getErrorMessage(result.error, 'Detect failed.') });
        return;
      }
      setDetectState({ loading: false });
    } catch (err: unknown) {
      setDetectState({ loading: false, error: getErrorMessage(err, 'Detect failed.') });
    }
  };

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-neutral-400">Loading server...</div>
      </div>
    );
  }

  if (error || !server) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-red-400">Failed to load server</div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <div className="flex items-center gap-2 text-neutral-400 mb-2">
            <ArrowLeft className="w-4 h-4" />
            <Link to="/" className="hover:text-emerald-400">Back to servers</Link>
          </div>
          <h1 className="text-3xl font-bold text-white">{server.name}</h1>
          <p className="text-neutral-400 mt-1">Manage server configuration, actions, and connectivity.</p>
        </div>
        <StatusBadge status={status?.connection_status || 'disconnected'} className="text-sm px-3 py-1.5" />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Connection</CardTitle>
          <CardDescription>SSH access details for this server.</CardDescription>
        </CardHeader>
        <CardContent className="grid grid-cols-1 md:grid-cols-3 gap-4 text-sm">
          <div>
            <p className="text-neutral-400">Host</p>
            <p className="text-white font-medium">{host}</p>
          </div>
          <div>
            <p className="text-neutral-400">Port</p>
            <p className="text-white font-medium">{port}</p>
          </div>
          <div>
            <p className="text-neutral-400">Username</p>
            <p className="text-white font-medium">{username}</p>
          </div>
        </CardContent>
      </Card>

      {/* Health Check Panel */}
      {status?.health_check && (
        <HealthCheckPanel healthCheck={status.health_check} />
      )}

      <Card>
        <CardHeader>
          <CardTitle>Runtime Control</CardTitle>
          <CardDescription>Start, stop, or restart the Hytale server with runtime options.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {actionState.error && (
            <div className="text-sm text-red-400">{actionState.error}</div>
          )}
          {status?.status === 'error' && status.error_message && (
            <div className="text-sm text-red-400">{status.error_message}</div>
          )}
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4 text-sm">
            <div>
              <p className="text-neutral-400 mb-1">Install directory</p>
              <Input
                value={runtimeOptions.install_dir}
                onChange={(event) => setRuntimeOptions((prev) => ({ ...prev, install_dir: event.target.value }))}
                placeholder="~/hytale-server"
              />
            </div>
            <div>
              <p className="text-neutral-400 mb-1">Service user</p>
              <Input
                value={runtimeOptions.service_user}
                onChange={(event) => setRuntimeOptions((prev) => ({ ...prev, service_user: event.target.value }))}
                placeholder="hytale"
              />
            </div>
            <div>
              <p className="text-neutral-400 mb-1">Java Xms</p>
              <Input
                value={runtimeOptions.java_xms}
                onChange={(event) => setRuntimeOptions((prev) => ({ ...prev, java_xms: event.target.value }))}
                placeholder="10G"
              />
            </div>
            <div>
              <p className="text-neutral-400 mb-1">Java Xmx</p>
              <Input
                value={runtimeOptions.java_xmx}
                onChange={(event) => setRuntimeOptions((prev) => ({ ...prev, java_xmx: event.target.value }))}
                placeholder="10G"
              />
            </div>
            <div>
              <p className="text-neutral-400 mb-1">Java metaspace</p>
              <Input
                value={runtimeOptions.java_metaspace}
                onChange={(event) => setRuntimeOptions((prev) => ({ ...prev, java_metaspace: event.target.value }))}
                placeholder="2560M"
              />
            </div>
            <div>
              <p className="text-neutral-400 mb-1">Assets path</p>
              <Input
                value={runtimeOptions.assets_path}
                onChange={(event) => setRuntimeOptions((prev) => ({ ...prev, assets_path: event.target.value }))}
                placeholder="~/hytale-server/Assets.zip"
              />
            </div>
          </div>

          <Button
            variant="ghost"
            size="sm"
            onClick={() => setRuntimeOptions((prev) => ({ ...prev, show_advanced: !prev.show_advanced }))}
          >
            {runtimeOptions.show_advanced ? 'Hide Advanced' : 'Show Advanced'}
          </Button>

          {runtimeOptions.show_advanced && (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4 text-sm">
              <div>
                <p className="text-neutral-400 mb-1">Backup directory</p>
                <Input
                  value={runtimeOptions.backup_dir}
                  onChange={(event) => setRuntimeOptions((prev) => ({ ...prev, backup_dir: event.target.value }))}
                  placeholder="~/hytale-server/Backups"
                />
              </div>
              <div>
                <p className="text-neutral-400 mb-1">Backup frequency (minutes)</p>
                  <Input
                    type="text"
                  value={runtimeOptions.backup_frequency}
                    onChange={(event) => setRuntimeOptions((prev) => ({ ...prev, backup_frequency: event.target.value }))}
                />
              </div>
              <div className="flex items-center gap-2">
                <input
                  id="runtime-enable-string-dedup"
                  type="checkbox"
                  checked={runtimeOptions.enable_string_dedup}
                  onChange={(event) => setRuntimeOptions((prev) => ({ ...prev, enable_string_dedup: event.target.checked }))}
                />
                <label htmlFor="runtime-enable-string-dedup" className="text-neutral-300">
                  Enable string deduplication
                </label>
              </div>
              <div className="flex items-center gap-2">
                <input
                  id="runtime-enable-aot"
                  type="checkbox"
                  checked={runtimeOptions.enable_aot}
                  onChange={(event) => setRuntimeOptions((prev) => ({ ...prev, enable_aot: event.target.checked }))}
                />
                <label htmlFor="runtime-enable-aot" className="text-neutral-300">
                  Enable AOT cache
                </label>
              </div>
              <div className="flex items-center gap-2">
                <input
                  id="runtime-enable-backup"
                  type="checkbox"
                  checked={runtimeOptions.enable_backup}
                  onChange={(event) => setRuntimeOptions((prev) => ({ ...prev, enable_backup: event.target.checked }))}
                />
                <label htmlFor="runtime-enable-backup" className="text-neutral-300">
                  Enable backups
                </label>
              </div>
              <div className="flex items-center gap-2 text-neutral-400">
                Running as the service user uses sudo internally.
              </div>
              <div>
                <p className="text-neutral-400 mb-1">Extra Java args</p>
                <Input
                  value={runtimeOptions.extra_java_args}
                  onChange={(event) => setRuntimeOptions((prev) => ({ ...prev, extra_java_args: event.target.value }))}
                  placeholder="-XX:+UseG1GC"
                />
              </div>
              <div>
                <p className="text-neutral-400 mb-1">Extra server args</p>
                <Input
                  value={runtimeOptions.extra_server_args}
                  onChange={(event) => setRuntimeOptions((prev) => ({ ...prev, extra_server_args: event.target.value }))}
                  placeholder="--arg value"
                />
              </div>
            </div>
          )}

          {actionState.success && (
            <div className="text-sm text-green-400">{actionState.success}</div>
          )}

          <div className="flex flex-wrap gap-2">
            <Button
              variant="primary"
              size="sm"
              isLoading={actionState.action === 'save'}
              onClick={saveRuntimeOptions}
            >
              <Save className="w-4 h-4 mr-1" />
              Save Configuration
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={resetRuntimeOptionsToDefaults}
            >
              Reset to Defaults
            </Button>
            <Button
              variant="secondary"
              size="sm"
              isLoading={actionState.action === 'start'}
              disabled={!canStart(status?.status)}
              onClick={() => runAction('start')}
            >
              <Play className="w-4 h-4 mr-1" />
              Start
            </Button>
            <Button
              variant="secondary"
              size="sm"
              isLoading={actionState.action === 'stop'}
              disabled={!canStop(status?.status)}
              onClick={() => runAction('stop')}
            >
              <Square className="w-4 h-4 mr-1" />
              Stop
            </Button>
            <Button
              variant="secondary"
              size="sm"
              isLoading={actionState.action === 'restart'}
              disabled={!canRestart(status?.status)}
              onClick={() => runAction('restart')}
            >
              <RotateCw className="w-4 h-4 mr-1" />
              Restart
            </Button>
            <Link to={`/console/${server.id}`}>
              <Button variant="ghost" size="sm">
                <Terminal className="w-4 h-4 mr-1" />
                Console
              </Button>
            </Link>
            <Link to={`/backups/${server.id}`}>
              <Button variant="ghost" size="sm">
                <Database className="w-4 h-4 mr-1" />
                Backups
              </Button>
            </Link>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Test SSH Connection</CardTitle>
          <CardDescription>Verify connectivity and fetch basic system info.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <Button variant="secondary" size="sm" onClick={testConnection} isLoading={testState.loading}>
            Run Test
          </Button>

          {testState.error && (
            <div className="text-sm text-red-400">{testState.error}</div>
          )}

          {testState.result && (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4 text-sm">
              <div>
                <p className="text-neutral-400">User</p>
                <p className="text-white font-medium">{testState.result.user || 'n/a'}</p>
              </div>
              <div>
                <p className="text-neutral-400">Hostname</p>
                <p className="text-white font-medium">{testState.result.hostname || 'n/a'}</p>
              </div>
              <div>
                <p className="text-neutral-400">OS</p>
                <p className="text-white font-medium break-all">{testState.result.os || 'n/a'}</p>
              </div>
              <div>
                <p className="text-neutral-400">Uptime</p>
                <p className="text-white font-medium">{testState.result.uptime || 'n/a'}</p>
              </div>
              {testState.result.metrics && (
                <>
                  <div>
                    <p className="text-neutral-400">CPU Usage</p>
                    <p className="text-white font-medium">{testState.result.metrics.cpu_usage?.toFixed(1) ?? 'n/a'}%</p>
                  </div>
                  <div>
                    <p className="text-neutral-400">Load (1m)</p>
                    <p className="text-white font-medium">
                      {testState.result.metrics.load1 !== undefined ? testState.result.metrics.load1.toFixed(2) : 'n/a'}
                    </p>
                  </div>
                  <div>
                    <p className="text-neutral-400">Memory</p>
                    <p className="text-white font-medium">
                      {testState.result.metrics.memory_used !== undefined && testState.result.metrics.memory_total !== undefined
                        ? `${formatBytes(testState.result.metrics.memory_used)} / ${formatBytes(testState.result.metrics.memory_total)}`
                        : 'n/a'}
                    </p>
                  </div>
                </>
              )}
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Live Metrics</CardTitle>
          <CardDescription>Real-time node_exporter snapshot (auto-refreshes every 30s).</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex items-center gap-2 mb-4">
            <Button variant="secondary" size="sm" onClick={() => refetchLiveMetrics()} isLoading={liveMetricsLoading}>
              Refresh
            </Button>
            {liveMetricsError && (
              <span className="text-xs text-red-400">Failed to load live metrics.</span>
            )}
          </div>
          {!liveMetric ? (
            <div className="text-sm text-neutral-400">No live metrics yet. Ensure node_exporter is reachable.</div>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4 text-sm">
              <div>
                <p className="text-neutral-400">CPU Usage</p>
                <p className="text-white font-medium">
                  {liveMetric.cpu_usage !== undefined ? `${Number(liveMetric.cpu_usage).toFixed(1)}%` : 'n/a'}
                </p>
              </div>
              <div>
                <p className="text-neutral-400">Memory</p>
                <p className="text-white font-medium">
                  {liveMetric.memory_used !== undefined && liveMetric.memory_total !== undefined
                    ? `${formatBytes(Number(liveMetric.memory_used))} / ${formatBytes(Number(liveMetric.memory_total))}`
                    : 'n/a'}
                </p>
              </div>
              <div>
                <p className="text-neutral-400">Disk</p>
                <p className="text-white font-medium">
                  {liveMetric.disk_used !== undefined && liveMetric.disk_total !== undefined
                    ? `${formatBytes(Number(liveMetric.disk_used))} / ${formatBytes(Number(liveMetric.disk_total))}`
                    : 'n/a'}
                </p>
              </div>
              <div className="md:col-span-3 text-xs text-neutral-500">
                Updated {formatRelativeTime(liveMetric.timestamp || new Date().toISOString())}
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Node exporter</CardTitle>
          <CardDescription>Install and detect node_exporter for metrics collection.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap gap-2">
            <Button
              variant="secondary"
              size="sm"
              isLoading={detectState.loading || nodeExporterLoading}
              onClick={detectNodeExporter}
            >
              Detect
            </Button>
            <Button
              variant="primary"
              size="sm"
              isLoading={nodeExporterState.installing}
              onClick={installNodeExporter}
            >
              Install
            </Button>
          </div>

          {nodeExporterState.error && (
            <div className="text-sm text-red-400">{nodeExporterState.error}</div>
          )}

          {detectState.error && (
            <div className="text-sm text-red-400">{detectState.error}</div>
          )}

          {nodeExporterError && (
            <div className="text-sm text-red-400">Failed to load status.</div>
          )}

          {nodeExporterStatus ? (
            <div className="grid grid-cols-1 md:grid-cols-4 gap-4 text-sm">
              <div>
                <p className="text-neutral-400">Installed</p>
                <p className="text-white font-medium">{nodeExporterStatus.installed ? 'Yes' : 'No'}</p>
              </div>
              <div>
                <p className="text-neutral-400">Running</p>
                <p className="text-white font-medium">{nodeExporterStatus.running ? 'Yes' : 'No'}</p>
              </div>
              <div>
                <p className="text-neutral-400">Enabled</p>
                <p className="text-white font-medium">{nodeExporterStatus.enabled ? 'Yes' : 'No'}</p>
              </div>
              <div>
                <p className="text-neutral-400">Version</p>
                <p className="text-white font-medium break-all">{nodeExporterStatus.version || 'n/a'}</p>
              </div>
              {nodeExporterStatus.url && (
                <div className="md:col-span-4">
                  <p className="text-neutral-400">Metrics URL</p>
                  <p className="text-white font-medium break-all">{nodeExporterStatus.url}</p>
                </div>
              )}
            </div>
          ) : (
            <div className="text-sm text-neutral-400">No node_exporter status yet. Click Detect.</div>
          )}

        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Agent Live State</CardTitle>
          <CardDescription>Live service, port, and Java process data from the agent.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex items-center gap-2">
            <Button variant="secondary" size="sm" onClick={() => refetchAgentState()} isLoading={agentStateLoading}>
              Refresh
            </Button>
            {agentStateError && (
              <span className="text-xs text-red-400">{getErrorMessage(agentStateError, 'Failed to load agent state.')}</span>
            )}
            {agentLiveState?.timestamp && (
              <span className="text-xs text-neutral-400">
                Updated {formatRelativeTime(new Date(agentLiveState.timestamp * 1000).toISOString())}
              </span>
            )}
          </div>

          {!agentLiveState ? (
            <div className="space-y-2">
              <div className="text-sm text-neutral-400">No agent state yet. Ensure the agent is installed.</div>
              {agentStateError && getAgentErrorDetails(agentStateError) && (
                <div className="text-xs text-neutral-400 whitespace-pre-wrap">
                  {`Agent status: ${getAgentErrorDetails(agentStateError)?.agent_status || 'n/a'}\nListening: ${getAgentErrorDetails(agentStateError)?.listening || 'n/a'}\nProcess: ${getAgentErrorDetails(agentStateError)?.process || 'n/a'}\nJournal: ${getAgentErrorDetails(agentStateError)?.journal || 'n/a'}`}
                </div>
              )}
            </div>
          ) : (
            <div className="space-y-6">
              <div className="grid grid-cols-1 md:grid-cols-3 gap-4 text-sm">
                <div>
                  <p className="text-neutral-400">Host UUID</p>
                  <p className="text-white font-medium break-all">{agentLiveState.host_uuid || 'n/a'}</p>
                </div>
                <div>
                  <p className="text-neutral-400">Open Ports</p>
                  <p className="text-white font-medium">
                    {Object.keys(agentLiveState.ports || {})
                      .filter((port) => agentLiveState.ports[port])
                      .sort((a, b) => Number(a) - Number(b))
                      .join(', ') || 'none'}
                  </p>
                </div>
                <div>
                  <p className="text-neutral-400">Java Processes</p>
                  <p className="text-white font-medium">{agentLiveState.java?.length ?? 0}</p>
                </div>
              </div>

              <div>
                <p className="text-sm text-neutral-300 mb-2">Services</p>
                {Object.keys(agentLiveState.services || {}).length === 0 ? (
                  <div className="text-xs text-neutral-400">No services tracked.</div>
                ) : (
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-2 text-xs">
                    {Object.entries(agentLiveState.services).map(([name, state]) => (
                      <div key={name} className="flex items-center justify-between border border-neutral-800 rounded-lg px-3 py-2">
                        <span className="text-neutral-300 break-all">{name}</span>
                        <span className="text-neutral-400">{String(state)}</span>
                      </div>
                    ))}
                  </div>
                )}
              </div>

              <div>
                <p className="text-sm text-neutral-300 mb-2">Java Processes</p>
                {agentLiveState.java.length === 0 ? (
                  <div className="text-xs text-neutral-400">No Java processes detected.</div>
                ) : (
                  <div className="space-y-3">
                    {agentLiveState.java.map((proc) => (
                      <div key={proc.pid} className="border border-neutral-800 rounded-lg p-3 text-xs text-neutral-300">
                        <div className="flex flex-wrap items-center justify-between gap-3 mb-2">
                          <div className="flex flex-wrap gap-3">
                            <span className="text-white">PID {proc.pid}</span>
                            <span>User: {proc.user || 'n/a'}</span>
                            <span>State: {proc.state}</span>
                            <span>RSS: {proc.rss}</span>
                            <span>Listen: {(proc.listen_ports || []).join(', ') || 'none'}</span>
                          </div>
                          <Button
                            variant="danger"
                            size="sm"
                            isLoading={killState.loading && killState.pid === proc.pid}
                            onClick={() => killProcess(proc.pid)}
                          >
                            Kill
                          </Button>
                        </div>
                        <div className="text-neutral-400 break-all">{proc.cmdline}</div>
                      </div>
                    ))}
                    {killState.error && (
                      <div className="text-xs text-red-400">{killState.error}</div>
                    )}
                    {killState.success && (
                      <div className="text-xs text-emerald-400">{killState.success}</div>
                    )}
                  </div>
                )}
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Monitoring Agent</CardTitle>
          <CardDescription>Install the lightweight monitoring agent and mTLS certs.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap gap-2">
            <Button
              variant="primary"
              size="sm"
              isLoading={agentInstallState.installing}
              onClick={installAgent}
            >
              Install Agent
            </Button>
            <Button
              variant="secondary"
              size="sm"
              disabled={!agentInstallState.installing}
              onClick={cancelAgentInstall}
            >
              Cancel
            </Button>
          </div>

          <label className="flex items-center gap-2 text-sm text-neutral-300">
            <input
              type="checkbox"
              className="h-4 w-4 rounded border-neutral-700 bg-neutral-900 text-emerald-500"
              checked={agentOptions.use_sudo}
              onChange={(event) => setAgentOptions((prev) => ({ ...prev, use_sudo: event.target.checked }))}
            />
            Use sudo
          </label>

          {agentInstallState.error && (
            <div className="text-sm text-red-400">{agentInstallState.error}</div>
          )}

          {agentInstallState.visible && (
            <div className="bg-neutral-950 border border-neutral-800 rounded-xl shadow-xl overflow-hidden">
              <div className="flex items-center justify-between px-4 py-3 border-b border-neutral-800">
                <div>
                  <p className="text-sm font-semibold text-white">Agent install</p>
                  <p className="text-xs text-neutral-400">{agentInstallState.installing ? 'Installing...' : 'Complete'}</p>
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={!agentInstallState.installing}
                    onClick={cancelAgentInstall}
                  >
                    Cancel
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => setAgentInstallState((prev) => ({ ...prev, expanded: !prev.expanded }))}
                  >
                    {agentInstallState.expanded ? 'Collapse' : 'Expand'}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setAgentInstallState((prev) => ({ ...prev, visible: false }))}
                  >
                    Close
                  </Button>
                </div>
              </div>
              <div className="px-4 py-3 space-y-2">
                <div className="text-xs text-neutral-400">Latest output</div>
                <div className="text-sm text-white break-words">
                  {agentInstallState.currentLine || 'Waiting for output...'}
                </div>
                {agentInstallState.error && (
                  <div className="text-xs text-red-400">{agentInstallState.error}</div>
                )}
              </div>
              {agentInstallState.expanded && (
                <div className="max-h-64 overflow-auto border-t border-neutral-800 bg-neutral-900/40 px-4 py-3 text-xs text-neutral-300 whitespace-pre-wrap">
                  {agentInstallState.outputLines.length === 0
                    ? 'No output yet.'
                    : agentInstallState.outputLines.join('\n')}
                </div>
              )}
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Server Dependencies</CardTitle>
          <CardDescription>Install Hytale server prerequisites (Debian/apt-get).</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap gap-2">
            <Button
              variant="primary"
              size="sm"
              isLoading={depsState.installing}
              onClick={installDependencies}
            >
              Install Dependencies
            </Button>
            <Button
              variant="secondary"
              size="sm"
              isLoading={depsCheck.loading}
              onClick={checkDependencies}
            >
              Check Dependencies
            </Button>
            <Button
              variant="secondary"
              size="sm"
              disabled={!depsState.installing}
              onClick={cancelDependenciesInstall}
            >
              Cancel
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setDepsOptions((prev) => ({ ...prev, show_advanced: !prev.show_advanced }))}
            >
              {depsOptions.show_advanced ? 'Hide Advanced' : 'Show Advanced'}
            </Button>
          </div>

          {depsOptions.show_advanced && (
            <div className="space-y-4">
              <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <label className="flex items-center gap-2 text-sm text-neutral-300">
                  <input
                    type="checkbox"
                    className="h-4 w-4 rounded border-neutral-700 bg-neutral-900 text-emerald-500"
                    checked={depsOptions.skip_update}
                    onChange={(event) =>
                      setDepsOptions((prev) => ({ ...prev, skip_update: event.target.checked }))
                    }
                  />
                  Skip system update
                </label>
                <label className="flex items-center gap-2 text-sm text-neutral-300">
                  <input
                    type="checkbox"
                    className="h-4 w-4 rounded border-neutral-700 bg-neutral-900 text-emerald-500"
                    checked={depsOptions.use_sudo}
                    onChange={(event) =>
                      setDepsOptions((prev) => ({ ...prev, use_sudo: event.target.checked }))
                    }
                  />
                  Use sudo
                </label>
                <label className="flex items-center gap-2 text-sm text-neutral-300">
                  <input
                    type="checkbox"
                    className="h-4 w-4 rounded border-neutral-700 bg-neutral-900 text-emerald-500"
                    checked={depsOptions.create_user}
                    onChange={(event) =>
                      setDepsOptions((prev) => ({ ...prev, create_user: event.target.checked }))
                    }
                  />
                  Create service user
                </label>
              </div>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <Input
                  label="Service User"
                  value={depsOptions.service_user}
                  onChange={(event) => setDepsOptions((prev) => ({ ...prev, service_user: event.target.value }))}
                  placeholder="hytale"
                />
                <Input
                  label="Service Groups (comma-separated)"
                  value={depsOptions.service_groups}
                  onChange={(event) => setDepsOptions((prev) => ({ ...prev, service_groups: event.target.value }))}
                  placeholder="sudo"
                />
              </div>
              <Input
                label="Install Directory"
                value={depsOptions.install_dir}
                onChange={(event) => setDepsOptions((prev) => ({ ...prev, install_dir: event.target.value }))}
                placeholder="~/hytale-server"
              />
              <label className="flex items-center gap-2 text-sm text-neutral-300">
                <input
                  type="checkbox"
                  className="h-4 w-4 rounded border-neutral-700 bg-neutral-900 text-emerald-500"
                  checked={depsOptions.save_config}
                  onChange={(event) =>
                    setDepsOptions((prev) => ({ ...prev, save_config: event.target.checked }))
                  }
                />
                Save these options to the server config
              </label>
            </div>
          )}

          {depsState.error && (
            <div className="text-sm text-red-400">{depsState.error}</div>
          )}

          {depsCheck.error && (
            <div className="text-sm text-red-400">{depsCheck.error}</div>
          )}

          {depsCheck.result && (
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4 text-sm">
              <div>
                <p className="text-neutral-400">Java</p>
                <p className="text-white font-medium">{depsCheck.result.java_ok ? 'OK' : 'Missing'}</p>
                <p className="text-xs text-neutral-500 break-all">{depsCheck.result.java_line || 'n/a'}</p>
              </div>
              <div>
                <p className="text-neutral-400">Service User</p>
                <p className="text-white font-medium">{depsCheck.result.user_ok ? 'OK' : 'Missing'}</p>
                <p className="text-xs text-neutral-500 break-all">{depsCheck.result.user_home || 'n/a'}</p>
              </div>
              <div>
                <p className="text-neutral-400">Install Directory</p>
                <p className="text-white font-medium">{depsCheck.result.dir_ok ? 'OK' : 'Missing'}</p>
                <p className="text-xs text-neutral-500 break-all">{depsCheck.result.dir_path || 'n/a'}</p>
              </div>
            </div>
          )}

          {depsState.visible && (
            <div className="bg-neutral-950 border border-neutral-800 rounded-xl shadow-xl overflow-hidden">
              <div className="flex items-center justify-between px-4 py-3 border-b border-neutral-800">
                <div>
                  <p className="text-sm font-semibold text-white">Dependency install</p>
                  <p className="text-xs text-neutral-400">{depsState.installing ? 'Installing...' : 'Complete'}</p>
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={!depsState.installing}
                    onClick={cancelDependenciesInstall}
                  >
                    Cancel
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => setDepsState((prev) => ({ ...prev, expanded: !prev.expanded }))}
                  >
                    {depsState.expanded ? 'Collapse' : 'Expand'}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setDepsState((prev) => ({ ...prev, visible: false }))}
                  >
                    Close
                  </Button>
                </div>
              </div>
              <div className="px-4 py-3 space-y-2">
                <div className="text-xs text-neutral-400">Latest output</div>
                <div className="text-sm text-white break-words">
                  {depsState.currentLine || 'Waiting for output...'}
                </div>
                {depsState.error && (
                  <div className="text-xs text-red-400">{depsState.error}</div>
                )}
              </div>
              {depsState.expanded && (
                <div className="max-h-64 overflow-auto border-t border-neutral-800 bg-neutral-900/40 px-4 py-3 text-xs text-neutral-300 whitespace-pre-wrap">
                  {depsState.outputLines.length === 0
                    ? 'No output yet.'
                    : depsState.outputLines.join('\n')}
                </div>
              )}
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Deploy Hytale Release</CardTitle>
          <CardDescription>Transfer and unpack a release to this server.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-neutral-300 mb-1.5">Release Package</label>
              <select
                className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white"
                value={deployOptions.package_name}
                onChange={(event) =>
                  setDeployOptions((prev) => ({ ...prev, package_name: event.target.value }))
                }
              >
                <option value="">Select a release</option>
                {(releases || []).map((release) => {
                  const name = release.file_path.split(/[\\/]/).pop() || release.version;
                  const packageName = name.replace(/\.zip$/i, '');
                  return (
                    <option key={release.id} value={packageName}>
                      {packageName} ({release.patchline})
                    </option>
                  );
                })}
              </select>
            </div>
            <div>
              <Input
                label="Install Directory"
                value={deployOptions.install_dir}
                onChange={(event) => setDeployOptions((prev) => ({ ...prev, install_dir: event.target.value }))}
                placeholder="~/hytale-server"
              />
            </div>
          </div>

          <div className="flex flex-wrap gap-2">
            <Button
              variant="primary"
              size="sm"
              isLoading={deployState.deploying}
              onClick={deployRelease}
            >
              Deploy Release
            </Button>
            <Button
              variant="secondary"
              size="sm"
              disabled={!deployState.deploying}
              onClick={cancelDeploy}
            >
              Cancel
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setDeployOptions((prev) => ({ ...prev, show_advanced: !prev.show_advanced }))}
            >
              {deployOptions.show_advanced ? 'Hide Advanced' : 'Show Advanced'}
            </Button>
          </div>

          {deployOptions.show_advanced && (
            <div className="space-y-4">
              <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Input
                  label="Service User"
                  value={deployOptions.service_user}
                  onChange={(event) => setDeployOptions((prev) => ({ ...prev, service_user: event.target.value }))}
                  placeholder="hytale"
                />
                <Input
                  label="Assets Path"
                  value={deployOptions.assets_path}
                  onChange={(event) => setDeployOptions((prev) => ({ ...prev, assets_path: event.target.value }))}
                  placeholder="/home/hytale/Assets.zip"
                />
                <Input
                  label="Backup Directory"
                  value={deployOptions.backup_dir}
                  onChange={(event) => setDeployOptions((prev) => ({ ...prev, backup_dir: event.target.value }))}
                  placeholder="/home/hytale/Backups"
                />
              </div>
              <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Input
                  label="Java Xms"
                  value={deployOptions.java_xms}
                  onChange={(event) => setDeployOptions((prev) => ({ ...prev, java_xms: event.target.value }))}
                />
                <Input
                  label="Java Xmx"
                  value={deployOptions.java_xmx}
                  onChange={(event) => setDeployOptions((prev) => ({ ...prev, java_xmx: event.target.value }))}
                />
                <Input
                  label="Max Metaspace"
                  value={deployOptions.java_metaspace}
                  onChange={(event) => setDeployOptions((prev) => ({ ...prev, java_metaspace: event.target.value }))}
                />
              </div>
              <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Input
                  label="Backup Frequency (minutes)"
                  type="number"
                  value={deployOptions.backup_frequency}
                  onChange={(event) =>
                    setDeployOptions((prev) => ({ ...prev, backup_frequency: Number(event.target.value) }))
                  }
                />
                <label className="flex items-center gap-2 text-sm text-neutral-300">
                  <input
                    type="checkbox"
                    className="h-4 w-4 rounded border-neutral-700 bg-neutral-900 text-emerald-500"
                    checked={deployOptions.enable_string_dedup}
                    onChange={(event) =>
                      setDeployOptions((prev) => ({ ...prev, enable_string_dedup: event.target.checked }))
                    }
                  />
                  Use String Dedup
                </label>
                <label className="flex items-center gap-2 text-sm text-neutral-300">
                  <input
                    type="checkbox"
                    className="h-4 w-4 rounded border-neutral-700 bg-neutral-900 text-emerald-500"
                    checked={deployOptions.enable_aot}
                    onChange={(event) => setDeployOptions((prev) => ({ ...prev, enable_aot: event.target.checked }))}
                  />
                  Enable AOT cache
                </label>
              </div>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <label className="flex items-center gap-2 text-sm text-neutral-300">
                  <input
                    type="checkbox"
                    className="h-4 w-4 rounded border-neutral-700 bg-neutral-900 text-emerald-500"
                    checked={deployOptions.enable_backup}
                    onChange={(event) =>
                      setDeployOptions((prev) => ({ ...prev, enable_backup: event.target.checked }))
                    }
                  />
                  Enable Hytale backups
                </label>
                <label className="flex items-center gap-2 text-sm text-neutral-300">
                  <input
                    type="checkbox"
                    className="h-4 w-4 rounded border-neutral-700 bg-neutral-900 text-emerald-500"
                    checked={deployOptions.use_sudo}
                    onChange={(event) => setDeployOptions((prev) => ({ ...prev, use_sudo: event.target.checked }))}
                  />
                  Use sudo
                </label>
              </div>
              <Input
                label="Extra Java Args"
                value={deployOptions.extra_java_args}
                onChange={(event) => setDeployOptions((prev) => ({ ...prev, extra_java_args: event.target.value }))}
                placeholder="-XX:+UseG1GC"
              />
              <Input
                label="Extra Server Args"
                value={deployOptions.extra_server_args}
                onChange={(event) => setDeployOptions((prev) => ({ ...prev, extra_server_args: event.target.value }))}
                placeholder="--disable-sentry"
              />
            </div>
          )}

          {deployState.error && (
            <div className="text-sm text-red-400">{deployState.error}</div>
          )}

          {deployState.visible && (
            <div className="bg-neutral-950 border border-neutral-800 rounded-xl shadow-xl overflow-hidden">
              <div className="flex items-center justify-between px-4 py-3 border-b border-neutral-800">
                <div>
                  <p className="text-sm font-semibold text-white">Release deployment</p>
                  <p className="text-xs text-neutral-400">{deployState.deploying ? 'Deploying...' : 'Complete'}</p>
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={!deployState.deploying}
                    onClick={cancelDeploy}
                  >
                    Cancel
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => setDeployState((prev) => ({ ...prev, expanded: !prev.expanded }))}
                  >
                    {deployState.expanded ? 'Collapse' : 'Expand'}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setDeployState((prev) => ({ ...prev, visible: false }))}
                  >
                    Close
                  </Button>
                </div>
              </div>
              <div className="px-4 py-3 space-y-2">
                <div className="text-xs text-neutral-400">Latest output</div>
                <div className="text-sm text-white break-words">
                  {deployState.currentLine || 'Waiting for output...'}
                </div>
                {deployState.error && (
                  <div className="text-xs text-red-400">{deployState.error}</div>
                )}
              </div>
              {deployState.expanded && (
                <div className="max-h-64 overflow-auto border-t border-neutral-800 bg-neutral-900/40 px-4 py-3 text-xs text-neutral-300 whitespace-pre-wrap">
                  {deployState.outputLines.length === 0
                    ? 'No output yet.'
                    : deployState.outputLines.join('\n')}
                </div>
              )}
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Transfer Benchmark</CardTitle>
          <CardDescription>Measure SFTP upload throughput to this server.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <Input
              label="Size (MB)"
              type="number"
              value={benchmarkOptions.size_mb}
              onChange={(event) =>
                setBenchmarkOptions((prev) => ({ ...prev, size_mb: Number(event.target.value) }))
              }
              placeholder="64"
            />
            <Input
              label="Block Size (MB)"
              type="number"
              value={benchmarkOptions.block_mb}
              onChange={(event) =>
                setBenchmarkOptions((prev) => ({ ...prev, block_mb: Number(event.target.value) }))
              }
              placeholder="4"
            />
            <label className="flex items-center gap-2 text-sm text-neutral-300">
              <input
                type="checkbox"
                className="h-4 w-4 rounded border-neutral-700 bg-neutral-900 text-emerald-500"
                checked={benchmarkOptions.remove_after}
                onChange={(event) =>
                  setBenchmarkOptions((prev) => ({ ...prev, remove_after: event.target.checked }))
                }
              />
              Remove test file after upload
            </label>
          </div>

          <div className="flex flex-wrap gap-2">
            <Button
              variant="primary"
              size="sm"
              isLoading={benchmarkState.running}
              onClick={runTransferBenchmark}
            >
              Run Benchmark
            </Button>
            <Button
              variant="secondary"
              size="sm"
              disabled={!benchmarkState.running}
              onClick={cancelBenchmark}
            >
              Cancel
            </Button>
          </div>

          {benchmarkState.error && (
            <div className="text-sm text-red-400">{benchmarkState.error}</div>
          )}

          {benchmarkState.visible && (
            <div className="bg-neutral-950 border border-neutral-800 rounded-xl shadow-xl overflow-hidden">
              <div className="flex items-center justify-between px-4 py-3 border-b border-neutral-800">
                <div>
                  <p className="text-sm font-semibold text-white">Transfer benchmark</p>
                  <p className="text-xs text-neutral-400">{benchmarkState.running ? 'Running...' : 'Complete'}</p>
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={!benchmarkState.running}
                    onClick={cancelBenchmark}
                  >
                    Cancel
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => setBenchmarkState((prev) => ({ ...prev, expanded: !prev.expanded }))}
                  >
                    {benchmarkState.expanded ? 'Collapse' : 'Expand'}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setBenchmarkState((prev) => ({ ...prev, visible: false }))}
                  >
                    Close
                  </Button>
                </div>
              </div>
              <div className="px-4 py-3 space-y-2">
                <div className="text-xs text-neutral-400">Latest output</div>
                <div className="text-sm text-white break-words font-mono">
                  {benchmarkState.currentLine || 'Waiting for output...'}
                </div>
                {benchmarkState.error && (
                  <div className="text-xs text-red-400">{benchmarkState.error}</div>
                )}
              </div>
              {benchmarkState.expanded && (
                <div className="max-h-64 overflow-auto border-t border-neutral-800 bg-neutral-900/40 px-4 py-3 text-xs text-neutral-300 whitespace-pre-wrap font-mono">
                  {benchmarkState.outputLines.length === 0
                    ? 'No output yet.'
                    : benchmarkState.outputLines.join('\n')}
                </div>
              )}
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Metrics History</CardTitle>
          <CardDescription>Recent performance snapshots captured during connection tests.</CardDescription>
        </CardHeader>
        <CardContent>
          {!metricsHistory || metricsHistory.length === 0 ? (
            <div className="text-sm text-neutral-400">No metrics captured yet. Run a connection test to record one.</div>
          ) : (
            <div className="space-y-3">
              {metricsHistory.map((metric, index) => (
                <div key={`${metric.timestamp}-${index}`} className="border border-neutral-800 rounded-lg p-3 text-sm">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="text-neutral-400">{formatRelativeTime(metric.timestamp)}</div>
                    <div className="text-xs text-neutral-500">{metric.status || 'unknown'}</div>
                  </div>
                  <div className="mt-2 grid grid-cols-1 md:grid-cols-3 gap-3">
                    <div>
                      <p className="text-neutral-400">CPU</p>
                      <p className="text-white font-medium">
                        {metric.cpu_usage !== undefined && metric.cpu_usage !== null
                          ? `${Number(metric.cpu_usage).toFixed(1)}%`
                          : 'n/a'}
                      </p>
                    </div>
                    <div>
                      <p className="text-neutral-400">Memory</p>
                      <p className="text-white font-medium">
                        {metric.memory_used !== undefined && metric.memory_total !== undefined
                          ? `${formatBytes(metric.memory_used)} / ${formatBytes(metric.memory_total)}`
                          : 'n/a'}
                      </p>
                    </div>
                    <div>
                      <p className="text-neutral-400">Disk</p>
                      <p className="text-white font-medium">
                        {metric.disk_used !== undefined && metric.disk_total !== undefined
                          ? `${formatBytes(metric.disk_used)} / ${formatBytes(metric.disk_total)}`
                          : 'n/a'}
                      </p>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Activity log</CardTitle>
          <CardDescription>Recent actions and background tasks for this server.</CardDescription>
        </CardHeader>
        <CardContent>
          {activityLoading ? (
            <div className="text-sm text-neutral-400">Loading activity...</div>
          ) : !activityLog || activityLog.length === 0 ? (
            <div className="text-sm text-neutral-400">No activity yet.</div>
          ) : (
            <div className="space-y-3">
              {activityLog.map((entry) => (
                <div key={`${entry.timestamp}-${entry.activity_type}`} className="border border-neutral-800 rounded-lg p-3 text-sm">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="text-neutral-400">{formatRelativeTime(entry.timestamp)}</div>
                    <div className={`text-xs ${entry.success ? 'text-emerald-400' : 'text-red-400'}`}>
                      {entry.activity_type}
                    </div>
                  </div>
                  <div className="mt-1 text-white">{entry.description}</div>
                  {entry.error_message && (
                    <div className="mt-1 text-xs text-red-400">{entry.error_message}</div>
                  )}
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
      {nodeExporterState.visible && (
        <div className="fixed bottom-6 right-6 z-50 max-w-lg w-full">
          <div className="bg-neutral-950 border border-neutral-800 rounded-xl shadow-xl overflow-hidden">
            <div className="flex items-center justify-between px-4 py-3 border-b border-neutral-800">
              <div>
                <p className="text-sm font-semibold text-white">Node exporter install</p>
                <p className="text-xs text-neutral-400">{nodeExporterState.installing ? 'Installing...' : 'Complete'}</p>
              </div>
              <div className="flex items-center gap-2">
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={!nodeExporterState.installing}
                  onClick={cancelInstall}
                >
                  Cancel
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => setNodeExporterState((prev) => ({ ...prev, expanded: !prev.expanded }))}
                >
                  {nodeExporterState.expanded ? 'Collapse' : 'Expand'}
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setNodeExporterState((prev) => ({ ...prev, visible: false }))}
                >
                  Close
                </Button>
              </div>
            </div>
            <div className="px-4 py-3 space-y-2">
              <div className="text-xs text-neutral-400">Latest output</div>
              <div className="text-sm text-white break-words">
                {nodeExporterState.currentLine || 'Waiting for output...'}
              </div>
              {nodeExporterState.error && (
                <div className="text-xs text-red-400">{nodeExporterState.error}</div>
              )}
            </div>
            {nodeExporterState.expanded && (
              <div className="max-h-64 overflow-auto border-t border-neutral-800 bg-neutral-900/40 px-4 py-3 text-xs text-neutral-300 whitespace-pre-wrap">
                {nodeExporterState.outputLines.length === 0
                  ? 'No output yet.'
                  : nodeExporterState.outputLines.join('\n')}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
