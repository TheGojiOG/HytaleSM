import React from 'react';

export type ConnectionStatus = 'disconnected' | 'online' | 'running';

interface StatusBadgeProps {
  status: ConnectionStatus;
  className?: string;
}

const statusConfig = {
  disconnected: {
    label: 'Disconnected',
    bgColor: 'bg-red-100',
    textColor: 'text-red-800',
    dotColor: 'bg-red-500',
  },
  online: {
    label: 'Online',
    bgColor: 'bg-yellow-100',
    textColor: 'text-yellow-800',
    dotColor: 'bg-yellow-500',
  },
  running: {
    label: 'Running',
    bgColor: 'bg-green-100',
    textColor: 'text-green-800',
    dotColor: 'bg-green-500',
  },
};

export const StatusBadge: React.FC<StatusBadgeProps> = ({ status, className = '' }) => {
  const config = statusConfig[status] || statusConfig.disconnected;

  return (
    <span
      className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium ${config.bgColor} ${config.textColor} ${className}`}
    >
      <span className={`w-2 h-2 rounded-full ${config.dotColor} animate-pulse`} />
      {config.label}
    </span>
  );
};

export default StatusBadge;
