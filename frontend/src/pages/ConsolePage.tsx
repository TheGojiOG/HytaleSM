import { useEffect, useMemo, useRef, useState } from 'react';
import { useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { serversApi } from '@/api';
import type { Server } from '@/api/types';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Input } from '@/components/Input';
import { Terminal } from 'lucide-react';
import { useAuth } from '@/contexts/AuthContext';

export function ConsolePage() {
  const { serverId } = useParams();
  const [selectedServerId, setSelectedServerId] = useState(serverId || '');
  const [lines, setLines] = useState<string[]>([]);
  const [command, setCommand] = useState('');
  const [canExecute, setCanExecute] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const wsRef = useRef<WebSocket | null>(null);
  const wsOpenedRef = useRef(false);
  const outputRef = useRef<HTMLDivElement | null>(null);
  const { isAuthenticated, isLoading: authLoading } = useAuth();

  const { data: servers } = useQuery<Server[]>({
    queryKey: ['servers'],
    queryFn: serversApi.listServers,
  });

  useEffect(() => {
    if (serverId) {
      setSelectedServerId(serverId);
    }
  }, [serverId]);

  useEffect(() => {
    if (!outputRef.current) {
      return;
    }
    outputRef.current.scrollTop = outputRef.current.scrollHeight;
  }, [lines]);

  const buildWsUrl = (path: string) => {
    const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws';
    const host = window.location.hostname;
    const port = window.location.port;
    const backendPort = port === '5173' ? '8080' : port;
    const hostWithPort = backendPort ? `${host}:${backendPort}` : host;
    return `${protocol}://${hostWithPort}${path}`;
  };

  const connect = (id: string) => {
    if (!id) {
      return;
    }
    if (!isAuthenticated && !authLoading) {
      setError('You must be logged in to view console output.');
      return;
    }
    setError(undefined);
    setLines([]);
    setCanExecute(false);

    const wsUrl = buildWsUrl(`/api/v1/ws/console/${id}`);
    if (wsRef.current && (wsRef.current.readyState === WebSocket.OPEN || wsRef.current.readyState === WebSocket.CONNECTING)) {
      if (wsRef.current.url === wsUrl) {
        return;
      }
      wsRef.current.close();
      wsRef.current = null;
    }
    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;
    wsOpenedRef.current = false;

    ws.onopen = () => {
      wsOpenedRef.current = true;
      setError(undefined);
    };

    ws.onmessage = (event) => {
      const raw = typeof event.data === 'string' ? event.data : '';
      const chunks = raw.split('\n').filter((chunk) => chunk.trim().length > 0);
      for (const chunk of chunks) {
        try {
          const msg = JSON.parse(chunk) as { type: string; payload?: any };
          if (msg.type === 'console_output') {
            const line = msg.payload?.line ?? '';
            if (!line) {
              continue;
            }
            setLines((prev) => {
              const next = prev.concat(line);
              return next.length > 1000 ? next.slice(next.length - 1000) : next;
            });
            continue;
          }
          if (msg.type === 'historical_output') {
            const incoming = Array.isArray(msg.payload?.lines) ? msg.payload.lines : [];
            if (incoming.length > 0) {
              setLines((prev) => {
                const merged = prev.concat(incoming);
                return merged.length > 1000 ? merged.slice(merged.length - 1000) : merged;
              });
            }
            continue;
          }
          if (msg.type === 'session_info') {
            setCanExecute(Boolean(msg.payload?.can_execute));
            continue;
          }
          if (msg.type === 'error') {
            setError(msg.payload?.message || 'Console error.');
          }
        } catch {
          // ignore malformed payloads
        }
      }
    };

    ws.onclose = () => {
      if (wsRef.current === ws) {
        wsRef.current = null;
      }
      if (!wsOpenedRef.current) {
        setError('Console connection closed before it was established. Check permissions or server status.');
      }
    };

    ws.onerror = () => {
      setError('Failed to connect to console WebSocket.');
    };
  };

  useEffect(() => {
    if (!selectedServerId) {
      return;
    }
    connect(selectedServerId);
    return () => {
      const ws = wsRef.current;
      if (!ws) {
        return;
      }
      if (ws.readyState === WebSocket.CONNECTING) {
        return;
      }
      ws.close();
      wsRef.current = null;
    };
  }, [selectedServerId]);

  const sendCommand = () => {
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) {
      return;
    }
    if (!command.trim()) {
      return;
    }
    wsRef.current.send(
      JSON.stringify({
        type: 'execute_command',
        payload: { command: command.trim() },
      }),
    );
    setCommand('');
  };

  const serverOptions = useMemo(() => servers ?? [], [servers]);

  return (
    <div>
      <div className="mb-6">
        <h1 className="text-3xl font-bold text-white">Console</h1>
        <p className="text-neutral-400 mt-1">Real-time server console access</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Console Access</CardTitle>
          <CardDescription>
            WebSocket-based real-time console streaming
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-col md:flex-row md:items-center gap-3">
            <select
              value={selectedServerId}
              onChange={(event) => setSelectedServerId(event.target.value)}
              className="bg-neutral-900 border border-neutral-700 rounded px-3 py-2 text-sm text-white"
            >
              <option value="">Select a server...</option>
              {serverOptions.map((server) => (
                <option key={server.id} value={server.id}>
                  {server.name}
                </option>
              ))}
            </select>
            <div className="flex items-center gap-2 text-xs text-neutral-400">
              <Terminal className="w-4 h-4" />
              {selectedServerId ? `Connected to ${selectedServerId}` : 'No server selected'}
            </div>
          </div>

          {error && <div className="text-sm text-red-400">{error}</div>}

          <div ref={outputRef} className="bg-neutral-950 border border-neutral-800 rounded p-3 h-80 overflow-y-auto overflow-x-auto text-xs text-neutral-200 font-mono whitespace-pre">
            {lines.length === 0 ? 'No console output yet.' : lines.join('\n')}
          </div>

          <div className="flex flex-col md:flex-row gap-2">
            <Input
              value={command}
              onChange={(event) => setCommand(event.target.value)}
              placeholder="Enter server command"
              disabled={!canExecute}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  sendCommand();
                }
              }}
            />
            <Button variant="secondary" size="sm" disabled={!canExecute} onClick={sendCommand}>
              Send
            </Button>
          </div>
          {!canExecute && selectedServerId && (
            <div className="text-xs text-neutral-500">You do not have permission to execute commands.</div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
