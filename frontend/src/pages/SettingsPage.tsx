import { useEffect, useMemo, useState } from 'react';
import { useQuery, useMutation } from '@tanstack/react-query';
import { settingsApi } from '@/api';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Input } from '@/components/Input';
import { Settings } from 'lucide-react';

const defaultSettings = {
  security: {
    rate_limit: {
      enabled: true,
      requests_per_minute: 60,
    },
    cors: {
      allowed_origins: ['http://localhost:5173'],
      allowed_methods: ['GET', 'POST', 'PUT', 'DELETE', 'OPTIONS'],
    },
    ssh: {
      known_hosts_path: './data/known_hosts',
      trust_on_first_use: true,
    },
  },
  logging: {
    level: 'info',
    format: 'json',
    file: '',
    max_size: 100,
    max_backups: 5,
    max_age: 30,
  },
  metrics: {
    enabled: true,
    default_interval: 60,
    retention_days: 2,
  },
  requires_restart: true,
};

export function SettingsPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ['settings'],
    queryFn: settingsApi.getSettings,
  });

  const [form, setForm] = useState(defaultSettings);
  const [savedMessage, setSavedMessage] = useState<string | null>(null);

  useEffect(() => {
    if (data) {
      setForm({
        ...data,
        requires_restart: data.requires_restart ?? true,
      });
    }
  }, [data]);

  const mutation = useMutation({
    mutationFn: settingsApi.updateSettings,
    onSuccess: (updated) => {
      setForm({
        ...updated,
        requires_restart: updated.requires_restart ?? true,
      });
      setSavedMessage('Settings saved. Some changes require a backend restart.');
      setTimeout(() => setSavedMessage(null), 5000);
    },
  });

  const originsText = useMemo(() => form.security.cors.allowed_origins.join('\n'), [form]);
  const methodsText = useMemo(() => form.security.cors.allowed_methods.join('\n'), [form]);

  const updateOrigins = (value: string) => {
    const items = value.split(/[\n,]+/).map((item) => item.trim()).filter(Boolean);
    setForm((prev) => ({
      ...prev,
      security: { ...prev.security, cors: { ...prev.security.cors, allowed_origins: items } },
    }));
  };

  const updateMethods = (value: string) => {
    const items = value.split(/[\n,]+/).map((item) => item.trim()).filter(Boolean);
    setForm((prev) => ({
      ...prev,
      security: { ...prev.security, cors: { ...prev.security.cors, allowed_methods: items } },
    }));
  };

  const handleSave = async () => {
    setSavedMessage(null);
    await mutation.mutateAsync(form);
  };

  return (
    <div>
      <div className="mb-6">
        <h1 className="text-3xl font-bold text-white">Settings</h1>
        <p className="text-neutral-400 mt-1">Configure application preferences</p>
      </div>

      {isLoading ? (
        <Card>
          <CardContent className="flex items-center justify-center py-12">
            <div className="text-neutral-400">Loading settings...</div>
          </CardContent>
        </Card>
      ) : error ? (
        <Card>
          <CardContent className="flex items-center justify-center py-12">
            <div className="text-red-400">Failed to load settings.</div>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-6">
          <Card>
            <CardHeader>
              <CardTitle>Security</CardTitle>
              <CardDescription>Configure rate limiting, CORS, and SSH trust options.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="flex items-center gap-3">
                <input
                  type="checkbox"
                  checked={form.security.rate_limit.enabled}
                  onChange={(event) =>
                    setForm((prev) => ({
                      ...prev,
                      security: {
                        ...prev.security,
                        rate_limit: { ...prev.security.rate_limit, enabled: event.target.checked },
                      },
                    }))
                  }
                  className="accent-emerald-500"
                />
                <span className="text-sm text-neutral-300">Enable rate limiting</span>
              </div>
              <Input
                label="Requests per minute"
                type="number"
                value={form.security.rate_limit.requests_per_minute}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    security: {
                      ...prev.security,
                      rate_limit: { ...prev.security.rate_limit, requests_per_minute: Number(event.target.value) },
                    },
                  }))
                }
              />

              <div>
                <label className="block text-sm font-medium text-neutral-300 mb-1.5">CORS allowed origins</label>
                <textarea
                  className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white"
                  rows={4}
                  value={originsText}
                  onChange={(event) => updateOrigins(event.target.value)}
                />
                <p className="text-xs text-neutral-500 mt-1">One per line or comma-separated.</p>
              </div>

              <div>
                <label className="block text-sm font-medium text-neutral-300 mb-1.5">CORS allowed methods</label>
                <textarea
                  className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white"
                  rows={3}
                  value={methodsText}
                  onChange={(event) => updateMethods(event.target.value)}
                />
              </div>

              <Input
                label="Known hosts path"
                value={form.security.ssh.known_hosts_path}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    security: {
                      ...prev.security,
                      ssh: { ...prev.security.ssh, known_hosts_path: event.target.value },
                    },
                  }))
                }
              />
              <div className="flex items-center gap-3">
                <input
                  type="checkbox"
                  checked={form.security.ssh.trust_on_first_use}
                  onChange={(event) =>
                    setForm((prev) => ({
                      ...prev,
                      security: {
                        ...prev.security,
                        ssh: { ...prev.security.ssh, trust_on_first_use: event.target.checked },
                      },
                    }))
                  }
                  className="accent-emerald-500"
                />
                <span className="text-sm text-neutral-300">Trust SSH host keys on first use</span>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Logging</CardTitle>
              <CardDescription>Configure log output and rotation.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <div>
                  <label className="block text-sm font-medium text-neutral-300 mb-1.5">Level</label>
                  <select
                    className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white"
                    value={form.logging.level}
                    onChange={(event) =>
                      setForm((prev) => ({
                        ...prev,
                        logging: { ...prev.logging, level: event.target.value },
                      }))
                    }
                  >
                    <option value="debug">debug</option>
                    <option value="info">info</option>
                    <option value="warn">warn</option>
                    <option value="error">error</option>
                  </select>
                </div>
                <div>
                  <label className="block text-sm font-medium text-neutral-300 mb-1.5">Format</label>
                  <select
                    className="w-full px-3 py-2 bg-neutral-900 border border-neutral-700 rounded-lg text-white"
                    value={form.logging.format}
                    onChange={(event) =>
                      setForm((prev) => ({
                        ...prev,
                        logging: { ...prev.logging, format: event.target.value },
                      }))
                    }
                  >
                    <option value="json">json</option>
                    <option value="text">text</option>
                  </select>
                </div>
              </div>
              <Input
                label="Log file path (optional)"
                value={form.logging.file}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    logging: { ...prev.logging, file: event.target.value },
                  }))
                }
              />
              <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Input
                  label="Max size (MB)"
                  type="number"
                  value={form.logging.max_size}
                  onChange={(event) =>
                    setForm((prev) => ({
                      ...prev,
                      logging: { ...prev.logging, max_size: Number(event.target.value) },
                    }))
                  }
                />
                <Input
                  label="Max backups"
                  type="number"
                  value={form.logging.max_backups}
                  onChange={(event) =>
                    setForm((prev) => ({
                      ...prev,
                      logging: { ...prev.logging, max_backups: Number(event.target.value) },
                    }))
                  }
                />
                <Input
                  label="Max age (days)"
                  type="number"
                  value={form.logging.max_age}
                  onChange={(event) =>
                    setForm((prev) => ({
                      ...prev,
                      logging: { ...prev.logging, max_age: Number(event.target.value) },
                    }))
                  }
                />
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Metrics</CardTitle>
              <CardDescription>Control background node_exporter collection.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="flex items-center gap-3">
                <input
                  type="checkbox"
                  checked={form.metrics.enabled}
                  onChange={(event) =>
                    setForm((prev) => ({
                      ...prev,
                      metrics: { ...prev.metrics, enabled: event.target.checked },
                    }))
                  }
                  className="accent-emerald-500"
                />
                <span className="text-sm text-neutral-300">Enable background metrics collection</span>
              </div>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <Input
                  label="Default interval (seconds)"
                  type="number"
                  value={form.metrics.default_interval}
                  onChange={(event) =>
                    setForm((prev) => ({
                      ...prev,
                      metrics: { ...prev.metrics, default_interval: Number(event.target.value) },
                    }))
                  }
                />
                <Input
                  label="Retention days"
                  type="number"
                  value={form.metrics.retention_days}
                  onChange={(event) =>
                    setForm((prev) => ({
                      ...prev,
                      metrics: { ...prev.metrics, retention_days: Number(event.target.value) },
                    }))
                  }
                />
              </div>
            </CardContent>
          </Card>

          <div className="flex flex-wrap items-center gap-3">
            <Button variant="primary" onClick={handleSave} isLoading={mutation.isPending}>
              Save Settings
            </Button>
            {savedMessage && <span className="text-sm text-emerald-400">{savedMessage}</span>}
            {mutation.isError && <span className="text-sm text-red-400">Failed to save settings.</span>}
          </div>
        </div>
      )}
      <div className="flex items-center gap-2 text-xs text-neutral-500 mt-6">
        <Settings className="w-4 h-4" />
        Some settings require a backend restart to take effect.
      </div>
    </div>
  );
}
