import type { HealthCheck } from '@/api/types';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/Card';
import { CheckCircle, XCircle, AlertCircle, Activity, Cpu, Wifi, Terminal } from 'lucide-react';

interface HealthCheckPanelProps {
  healthCheck: HealthCheck;
}

export function HealthCheckPanel({ healthCheck }: HealthCheckPanelProps) {
  const getStatusIcon = (status: boolean, size = 16) => {
    return status ? (
      <CheckCircle className="text-green-500" size={size} />
    ) : (
      <XCircle className="text-red-500" size={size} />
    );
  };

  const getConnectionStatusColor = (status: string) => {
    switch (status) {
      case 'running':
        return 'text-green-600';
      case 'online':
        return 'text-yellow-600';
      case 'disconnected':
        return 'text-red-600';
      default:
        return 'text-gray-600';
    }
  };

  const formatUptime = (seconds: number) => {
    if (!seconds || seconds === 0) return 'N/A';
    
    const hours = Math.floor(seconds / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);
    const secs = seconds % 60;
    
    if (hours > 0) {
      return `${hours}h ${minutes}m`;
    } else if (minutes > 0) {
      return `${minutes}m ${secs}s`;
    } else {
      return `${secs}s`;
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Activity size={20} />
          Health Status
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {/* Overall Status */}
        <div className="flex items-center justify-between p-3 bg-gray-50 dark:bg-gray-800 rounded-lg">
          <span className="font-medium">Overall Status</span>
          <span className={`font-semibold capitalize ${getConnectionStatusColor(healthCheck.connection_status)}`}>
            {healthCheck.connection_status}
          </span>
        </div>

        {/* SSH Status */}
        <div className="space-y-2">
          <div className="flex items-center gap-2 font-medium">
            <Wifi size={16} />
            SSH Connection
          </div>
          <div className="pl-6 space-y-1 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-gray-600 dark:text-gray-400">Status</span>
              <div className="flex items-center gap-2">
                {getStatusIcon(healthCheck.ssh.connected)}
                <span>{healthCheck.ssh.connected ? 'Connected' : 'Disconnected'}</span>
              </div>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-gray-600 dark:text-gray-400">Host</span>
              <span className="font-mono">{healthCheck.ssh.host}:{healthCheck.ssh.port}</span>
            </div>
            {healthCheck.ssh.error && (
              <div className="flex items-center gap-2 text-red-600">
                <AlertCircle size={14} />
                <span>{healthCheck.ssh.error}</span>
              </div>
            )}
          </div>
        </div>

        {/* Process Status */}
        <div className="space-y-2">
          <div className="flex items-center gap-2 font-medium">
            <Cpu size={16} />
            Server Process
          </div>
          <div className="pl-6 space-y-1 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-gray-600 dark:text-gray-400">Running</span>
              <div className="flex items-center gap-2">
                {getStatusIcon(healthCheck.process.running)}
                <span>{healthCheck.process.running ? 'Yes' : 'No'}</span>
              </div>
            </div>
            {healthCheck.process.pid && (
              <div className="flex items-center justify-between">
                <span className="text-gray-600 dark:text-gray-400">PID</span>
                <span className="font-mono">{healthCheck.process.pid}</span>
              </div>
            )}
            {healthCheck.process.port && (
              <div className="flex items-center justify-between">
                <span className="text-gray-600 dark:text-gray-400">Port</span>
                <span className="font-mono">{healthCheck.process.port}</span>
              </div>
            )}
            {healthCheck.process.uptime_seconds > 0 && (
              <div className="flex items-center justify-between">
                <span className="text-gray-600 dark:text-gray-400">Uptime</span>
                <span>{formatUptime(healthCheck.process.uptime_seconds)}</span>
              </div>
            )}
            {healthCheck.process.detection_method && (
              <div className="flex items-center justify-between">
                <span className="text-gray-600 dark:text-gray-400">Detection</span>
                <span className="text-xs px-2 py-0.5 bg-blue-100 dark:bg-blue-900 text-blue-800 dark:text-blue-200 rounded">
                  {healthCheck.process.detection_method}
                </span>
              </div>
            )}
          </div>
        </div>

        {/* Screen Status */}
        <div className="space-y-2">
          <div className="flex items-center gap-2 font-medium">
            <Terminal size={16} />
            Screen Session
          </div>
          <div className="pl-6 space-y-1 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-gray-600 dark:text-gray-400">Session</span>
              <span className="font-mono">{healthCheck.screen.session_name}</span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-gray-600 dark:text-gray-400">Exists</span>
              <div className="flex items-center gap-2">
                {getStatusIcon(healthCheck.screen.session_exists)}
                <span>{healthCheck.screen.session_exists ? 'Yes' : 'No'}</span>
              </div>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-gray-600 dark:text-gray-400">Streaming</span>
              <div className="flex items-center gap-2">
                {getStatusIcon(healthCheck.screen.streaming)}
                <span>{healthCheck.screen.streaming ? 'Yes' : 'No'}</span>
              </div>
            </div>
          </div>
        </div>

        {/* Agent Status */}
        <div className="space-y-2">
          <div className="flex items-center gap-2 font-medium">
            <Activity size={16} />
            Monitoring Agent
          </div>
          <div className="pl-6 space-y-1 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-gray-600 dark:text-gray-400">Available</span>
              <div className="flex items-center gap-2">
                {getStatusIcon(healthCheck.agent.available)}
                <span>{healthCheck.agent.available ? 'Yes' : 'No'}</span>
              </div>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-gray-600 dark:text-gray-400">Connected</span>
              <div className="flex items-center gap-2">
                {getStatusIcon(healthCheck.agent.connected)}
                <span>{healthCheck.agent.connected ? 'Yes' : 'No'}</span>
              </div>
            </div>
            {healthCheck.agent.error && (
              <div className="flex items-center gap-2 text-yellow-600">
                <AlertCircle size={14} />
                <span>{healthCheck.agent.error}</span>
              </div>
            )}
            {healthCheck.agent.java_processes && healthCheck.agent.java_processes.length > 0 && (
              <div className="mt-2">
                <div className="text-gray-600 dark:text-gray-400 mb-1">Java Processes ({healthCheck.agent.java_processes.length})</div>
                {healthCheck.agent.java_processes.map((proc, idx) => (
                  <div key={idx} className="pl-2 py-1 text-xs bg-gray-50 dark:bg-gray-800 rounded mb-1">
                    <div className="font-mono">PID: {proc.pid} | User: {proc.user}</div>
                    {proc.listen_ports && proc.listen_ports.length > 0 && (
                      <div className="text-gray-500">Ports: {proc.listen_ports.join(', ')}</div>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
