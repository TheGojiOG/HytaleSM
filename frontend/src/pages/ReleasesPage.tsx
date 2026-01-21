import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import axios from 'axios';
import { releasesApi, type Release, type ReleaseJob } from '@/api';
import { getErrorMessage } from '@/api/client';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Input } from '@/components/Input';
import { AuthPromptModal } from '@/components/AuthPromptModal';
import { formatBytes, formatRelativeTime } from '@/utils/format';
import { useAuth } from '@/contexts/AuthContext';

interface StreamEvent {
  event: string;
  data: string;
}

export function ReleasesPage() {
  const queryClient = useQueryClient();
  const { isAuthenticated, isLoading: authLoading } = useAuth();
  const [patchline, setPatchline] = useState('default');
  const [activeJob, setActiveJob] = useState<ReleaseJob | null>(null);
  const [jobLogs, setJobLogs] = useState<string[]>([]);
  const [jobStatus, setJobStatus] = useState<string>('');
  const [authUrl, setAuthUrl] = useState<string | undefined>();
  const [authCode, setAuthCode] = useState<string | undefined>();
  const [showAuth, setShowAuth] = useState(false);
  const [jobError, setJobError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionNotice, setActionNotice] = useState<string | null>(null);
  const [downloaderMissing, setDownloaderMissing] = useState(false);
  const [showRemoved, setShowRemoved] = useState(false);
  const jobSocketRef = useRef<WebSocket | null>(null);
  const logsRef = useRef<HTMLDivElement | null>(null);
  const [downloaderAuth, setDownloaderAuth] = useState<{ exists: boolean; expiresAt?: number; branch?: string }>({
    exists: false,
  });

  const { data: releases, isLoading, error } = useQuery({
    queryKey: ['releases', { includeRemoved: showRemoved }],
    queryFn: () => releasesApi.listReleases(showRemoved),
  });

  useEffect(() => {
    return () => {
      jobSocketRef.current?.close();
      jobSocketRef.current = null;
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    const checkDownloader = async () => {
      try {
        const status = await releasesApi.downloaderStatus();
        if (!cancelled) {
          setDownloaderMissing(!status.exists);
        }
      } catch {
        if (!cancelled) {
          setDownloaderMissing(true);
        }
      }
    };

    checkDownloader();
    return () => {
      cancelled = true;
    };
  }, []);

  const refreshAuthStatus = async () => {
    try {
      const status = await releasesApi.downloaderAuthStatus();
      setDownloaderAuth({
        exists: status.exists,
        expiresAt: status.expires_at,
        branch: status.branch,
      });
    } catch {
      setDownloaderAuth({ exists: false });
    }
  };

  useEffect(() => {
    let cancelled = false;
    const checkAuth = async () => {
      try {
        const status = await releasesApi.downloaderAuthStatus();
        if (!cancelled) {
          setDownloaderAuth({
            exists: status.exists,
            expiresAt: status.expires_at,
            branch: status.branch,
          });
        }
      } catch {
        if (!cancelled) {
          setDownloaderAuth({ exists: false });
        }
      }
    };

    checkAuth();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (showAuth) {
      return;
    }

    if (jobStatus === 'complete') {
      refreshAuthStatus();
    }
  }, [jobStatus, showAuth]);

  useEffect(() => {
    if (!showAuth || !activeJob) {
      return;
    }
    const interval = setInterval(() => {
      refreshJob(activeJob.id);
    }, 1000);
    return () => {
      clearInterval(interval);
    };
  }, [showAuth, activeJob]);

  const isTerminalStatus = (status?: string) => status === 'complete' || status === 'failed';

  useEffect(() => {
    if (!activeJob || showAuth) {
      return;
    }
    const status = jobStatus || activeJob.status;
    if (isTerminalStatus(status)) {
      return;
    }
    const interval = setInterval(() => {
      refreshJob(activeJob.id);
    }, 3000);
    return () => {
      clearInterval(interval);
    };
  }, [activeJob, jobStatus, showAuth]);

  useEffect(() => {
    if (!logsRef.current) {
      return;
    }
    logsRef.current.scrollTop = logsRef.current.scrollHeight;
  }, [jobLogs, jobStatus, activeJob]);

  const hydrateJob = (job: ReleaseJob) => {
    setActiveJob(job);
    setJobLogs(job.output || []);
    setJobStatus(job.status);
    setJobError(job.error ?? null);
    setAuthUrl(job.auth_url);
    setAuthCode(job.auth_code);
    setShowAuth(Boolean(job.needs_auth) && !isTerminalStatus(job.status));
  };

  const refreshJob = async (jobId: string) => {
    try {
      const latest = await releasesApi.getJob(jobId);
      hydrateJob(latest);
      if (latest.status === 'complete' || latest.status === 'failed') {
        queryClient.invalidateQueries({ queryKey: ['releases'] });
        setShowAuth(false);
      }
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.status === 401) {
        setActionError('Session expired. Please sign in again.');
        setShowAuth(false);
        return;
      }
      setJobError(getErrorMessage(err));
    }
  };

  const handleStreamEvent = (event: StreamEvent) => {
    if (event.event === 'log') {
      setJobLogs((prev) => [...prev, event.data]);
    }
    if (event.event === 'status') {
      setJobStatus(event.data);
      if (event.data === 'complete' || event.data === 'failed') {
        setShowAuth(false);
        queryClient.invalidateQueries({ queryKey: ['releases'] });
        jobSocketRef.current?.close();
      }
    }
    if (event.event === 'auth') {
      try {
        const payload = JSON.parse(event.data);
        setAuthUrl(payload.auth_url);
        setAuthCode(payload.auth_code);
      } catch {
        const [url, code] = event.data.split('|');
        setAuthUrl(url);
        setAuthCode(code);
      }
      setShowAuth(true);
    }
  };

  const buildWsUrl = (path: string) => {
    const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws';
    const host = window.location.hostname;
    const port = window.location.port;
    const backendPort = port === '5173' ? '8080' : port;
    const hostWithPort = backendPort ? `${host}:${backendPort}` : host;
    return `${protocol}://${hostWithPort}${path}`;
  };

  const connectJobSocket = (jobId: string) => {
    if (!isAuthenticated && !authLoading) {
      setJobError('Authentication required to stream job output.');
      return;
    }

    jobSocketRef.current?.close();

    const wsUrl = buildWsUrl(`/api/v1/ws/releases/jobs/${jobId}`);
    const socket = new WebSocket(wsUrl);
    jobSocketRef.current = socket;

    socket.onmessage = (event) => {
      const raw = typeof event.data === 'string' ? event.data : '';
      if (!raw) {
        return;
      }
      const messages = raw.split('\n').filter(Boolean);
      for (const message of messages) {
        try {
          const parsed = JSON.parse(message) as { type?: string; payload?: any };
          if (parsed.type !== 'release_job_event') {
            continue;
          }
          if (parsed.payload?.job_id !== jobId) {
            continue;
          }
          if (typeof parsed.payload?.event === 'string' && typeof parsed.payload?.data === 'string') {
            handleStreamEvent({ event: parsed.payload.event, data: parsed.payload.data });
          }
        } catch {
          // ignore malformed messages
        }
      }
    };

    socket.onerror = () => {
      setJobError('Streaming failed. Refreshing job status.');
      void refreshJob(jobId);
    };

    socket.onclose = () => {
      if (jobSocketRef.current === socket) {
        jobSocketRef.current = null;
      }
    };
  };

  useEffect(() => {
    let cancelled = false;
    const loadLatestJob = async () => {
      try {
        const jobs = await releasesApi.listJobs();
        if (cancelled || jobs.length === 0) {
          return;
        }
        const latest = [...jobs].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())[0];
        hydrateJob(latest);
        if (!isTerminalStatus(latest.status)) {
          connectJobSocket(latest.id);
        }
      } catch {
        // ignore resume failures
      }
    };
    loadLatestJob();
    return () => {
      cancelled = true;
    };
  }, []);

  const startJobStream = async (job: ReleaseJob) => {
    hydrateJob(job);
    setActionError(null);
    setActionNotice(null);
    setJobError(null);
    connectJobSocket(job.id);
  };

  const handleDownload = async () => {
    setActionError(null);
    setActionNotice(null);
    try {
      const job = await releasesApi.downloadRelease({ patchline });
      await startJobStream(job);
    } catch (err) {
      setActionError(getErrorMessage(err));
    }
  };

  const handleResetAuth = async () => {
    setActionError(null);
    setActionNotice(null);
    try {
      await releasesApi.resetAuth();
      setActionNotice('Downloader credentials cleared. Please authenticate again.');
    } catch (err) {
      setActionError(getErrorMessage(err));
    }
  };

  const handleInitDownloader = async (force: boolean) => {
    setActionError(null);
    setActionNotice(null);
    try {
      const job = await releasesApi.initDownloader({ force });
      await startJobStream(job);
    } catch (err) {
      setActionError(getErrorMessage(err));
    }
  };

  const handleInitAuth = async () => {
    setActionError(null);
    setActionNotice(null);
    try {
      const job = await releasesApi.printVersion({ patchline });
      await startJobStream(job);
    } catch (err) {
      setActionError(getErrorMessage(err));
    }
  };

  const renderAuthExpiry = () => {
    if (!downloaderAuth.expiresAt) {
      return 'Unknown';
    }
    const date = new Date(downloaderAuth.expiresAt * 1000);
    return date.toLocaleString();
  };

  const handleDeleteRelease = async (release: Release) => {
    setActionError(null);
    setActionNotice(null);
    if (!window.confirm(`Delete release ${release.version}? This will remove the file and DB entry.`)) {
      return;
    }
    try {
      await releasesApi.deleteRelease(release.id);
      await queryClient.invalidateQueries({ queryKey: ['releases'] });
    } catch (err) {
      setActionError(getErrorMessage(err));
    }
  };

  const getPackageName = (release: Release | undefined) => {
    if (!release?.file_path) {
      return '';
    }
    const parts = release.file_path.split(/[\\/]/);
    const name = parts[parts.length - 1] || '';
    return name.replace(/\.zip$/i, '');
  };

  const getVersionSuffix = (packageName: string) => {
    const match = packageName.match(/^\d{4}\.\d{2}\.\d{2}-(.+)$/);
    return match ? match[1] : packageName;
  };

  const renderDateLabel = (release: Release | undefined) => {
    if (!release?.downloaded_at) {
      return 'Unknown';
    }
    return formatRelativeTime(release.downloaded_at);
  };

  const hasReleases = releases && releases.length > 0;
  const latestRelease = useMemo(() => releases?.[0], [releases]);
  const filteredReleases = useMemo(() => {
    if (!releases) {
      return [];
    }
    return showRemoved ? releases : releases.filter((release) => !release.removed);
  }, [releases, showRemoved]);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-64 text-neutral-400">Loading releases...</div>
    );
  }

  if (error) {
    return (
      <div className="flex items-center justify-center h-64 text-red-400">Failed to load releases.</div>
    );
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold text-white">Releases</h1>
        <p className="text-neutral-400 mt-1">Manage Hytale release packages stored on the control server.</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Downloader</CardTitle>
          <CardDescription>Install or update the Hytale downloader used for release management.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {downloaderMissing ? (
            <div className="rounded-lg border border-amber-600/40 bg-amber-950/30 px-3 py-2 text-sm text-amber-200">
              Downloader not found. Install it before downloading releases.
            </div>
          ) : (
            <div className="rounded-lg border border-emerald-600/40 bg-emerald-950/30 px-3 py-2 text-sm text-emerald-200">
              Downloader is available.
            </div>
          )}
          {actionNotice && <div className="text-sm text-emerald-400">{actionNotice}</div>}
          {actionError && <div className="text-sm text-red-400">{actionError}</div>}
          <div className="flex flex-wrap gap-3">
            <Button variant="secondary" onClick={() => handleInitDownloader(false)}>
              Install Downloader
            </Button>
            <Button variant="secondary" onClick={() => handleInitDownloader(true)}>
              Update Downloader
            </Button>
            <Button variant="secondary" onClick={handleResetAuth}>
              Reset Auth
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Downloader Auth</CardTitle>
          <CardDescription>Authenticate the downloader to access releases.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {downloaderAuth.exists ? (
            <div className="space-y-1 text-sm text-neutral-300">
              <div>
                <span className="text-neutral-400">Branch:</span> {downloaderAuth.branch || 'unknown'}
              </div>
              <div>
                <span className="text-neutral-400">Expires:</span> {renderAuthExpiry()}
              </div>
            </div>
          ) : (
            <div className="rounded-lg border border-amber-600/40 bg-amber-950/30 px-3 py-2 text-sm text-amber-200">
              No downloader auth found. Initialize auth to continue.
            </div>
          )}
          <div className="flex flex-wrap gap-3">
            <Button variant="primary" onClick={handleInitAuth}>
              Login / Init Auth
            </Button>
            <Button variant="secondary" onClick={handleResetAuth}>
              Reset Auth
            </Button>
          </div>
        </CardContent>
      </Card>

      {!hasReleases && (
        <Card>
          <CardHeader>
            <CardTitle>No releases yet</CardTitle>
            <CardDescription>
              Initialize the downloader and fetch the first release package. Authentication may be required.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <Input
              label="Patchline"
              value={patchline}
              onChange={(event) => setPatchline(event.target.value)}
              placeholder="default"
            />
            <div className="flex flex-wrap gap-3">
              <Button variant="primary" onClick={handleDownload}>
                Initialize & Download
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      {hasReleases && (
        <Card>
          <CardHeader>
            <CardTitle>Latest Release</CardTitle>
            <CardDescription>Most recently downloaded package.</CardDescription>
          </CardHeader>
          <CardContent className="grid grid-cols-1 md:grid-cols-3 gap-4 text-sm">
            <div>
              <p className="text-neutral-400">Version</p>
              <p className="text-white font-medium">{getVersionSuffix(getPackageName(latestRelease))}</p>
            </div>
            <div>
              <p className="text-neutral-400">Package</p>
              <p className="text-white font-medium break-all">{getPackageName(latestRelease)}</p>
            </div>
            <div>
              <p className="text-neutral-400">Patchline</p>
              <p className="text-white font-medium">{latestRelease?.patchline}</p>
            </div>
            <div>
              <p className="text-neutral-400">Size</p>
              <p className="text-white font-medium">{formatBytes(latestRelease?.file_size ?? 0)}</p>
            </div>
            <div>
              <p className="text-neutral-400">SHA256</p>
              <p className="text-white font-medium break-all">{latestRelease?.sha256}</p>
            </div>
            <div>
              <p className="text-neutral-400">{latestRelease?.source === 'user_added' ? 'Discovered' : 'Downloaded'}</p>
              <p className="text-white font-medium">{renderDateLabel(latestRelease)}</p>
            </div>
            <div className="md:col-span-2">
              <p className="text-neutral-400">File</p>
              <p className="text-white font-medium break-all">{latestRelease?.file_path}</p>
            </div>
            <div>
              <p className="text-neutral-400">Source</p>
              <p className="text-white font-medium">{latestRelease?.source || 'downloaded'}</p>
            </div>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle>All Releases</CardTitle>
          <CardDescription>Manage downloaded and user-added releases.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <label className="flex items-center gap-2 text-sm text-neutral-300">
            <input
              type="checkbox"
              className="h-4 w-4 rounded border-neutral-700 bg-neutral-900 text-emerald-500"
              checked={showRemoved}
              onChange={(event) => setShowRemoved(event.target.checked)}
            />
            Show removed releases
          </label>
          <div className="overflow-auto rounded-lg border border-neutral-800">
            <table className="w-full text-left text-xs text-neutral-300">
              <thead className="bg-neutral-900/60 text-neutral-400">
                <tr>
                  <th className="px-3 py-2">Package</th>
                  <th className="px-3 py-2">Version</th>
                  <th className="px-3 py-2">Patchline</th>
                  <th className="px-3 py-2">Source</th>
                  <th className="px-3 py-2">Status</th>
                  <th className="px-3 py-2">SHA256</th>
                  <th className="px-3 py-2">Date</th>
                  <th className="px-3 py-2"></th>
                </tr>
              </thead>
              <tbody>
                {filteredReleases.length === 0 && (
                  <tr>
                    <td colSpan={8} className="px-3 py-4 text-center text-neutral-500">
                      No releases found.
                    </td>
                  </tr>
                )}
                {filteredReleases.map((release) => {
                  const packageName = getPackageName(release);
                  return (
                    <tr key={release.id} className="border-t border-neutral-800">
                      <td className="px-3 py-2 text-white break-all">{packageName}</td>
                      <td className="px-3 py-2">{getVersionSuffix(packageName)}</td>
                      <td className="px-3 py-2">{release.patchline}</td>
                      <td className="px-3 py-2">{release.source || 'downloaded'}</td>
                      <td className="px-3 py-2">
                        {release.removed ? 'removed' : release.status}
                      </td>
                      <td className="px-3 py-2 break-all">{release.sha256}</td>
                      <td className="px-3 py-2">
                        {release.source === 'user_added' ? 'Discovered' : 'Downloaded'} {renderDateLabel(release)}
                      </td>
                      <td className="px-3 py-2 text-right">
                        <Button variant="danger" size="sm" onClick={() => handleDeleteRelease(release)}>
                          Delete
                        </Button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Download Release</CardTitle>
          <CardDescription>Fetch a new release for a specific patchline.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <Input
            label="Patchline"
            value={patchline}
            onChange={(event) => setPatchline(event.target.value)}
            placeholder="default"
          />
          {actionNotice && <div className="text-sm text-emerald-400">{actionNotice}</div>}
          {actionError && <div className="text-sm text-red-400">{actionError}</div>}
          <div className="flex flex-wrap gap-3">
            <Button variant="primary" onClick={handleDownload}>
              Download
            </Button>
            <Button variant="secondary" onClick={handleResetAuth}>
              Reset Auth
            </Button>
          </div>
        </CardContent>
      </Card>

      {activeJob && (
        <Card>
          <CardHeader>
            <CardTitle>Job Status</CardTitle>
            <CardDescription>Live output from the downloader.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="text-sm text-neutral-400">Status: {jobStatus || activeJob.status}</div>
            {(jobError || activeJob.error) && (
              <div className="space-y-2 text-sm">
                <div className="text-red-400">{jobError || activeJob.error}</div>
                {(jobError || activeJob.error)?.toLowerCase().includes('authentication failed') && (
                  <div className="text-amber-400">
                    Auth failed. Use Reset Auth, then try again.
                  </div>
                )}
              </div>
            )}
            <div
              ref={logsRef}
              className="max-h-64 overflow-auto rounded-lg border border-neutral-800 bg-neutral-900/40 p-3 text-xs text-neutral-300 whitespace-pre-wrap"
            >
              {jobLogs.length === 0 ? 'No output yet.' : jobLogs.join('\n')}
            </div>
          </CardContent>
        </Card>
      )}

      <AuthPromptModal
        isOpen={showAuth}
        authUrl={authUrl}
        authCode={authCode}
        onClose={() => setShowAuth(false)}
        onContinue={() => {
          if (activeJob) {
            refreshJob(activeJob.id);
          }
        }}
        title="Hytale Downloader Authentication"
      />
    </div>
  );
}
