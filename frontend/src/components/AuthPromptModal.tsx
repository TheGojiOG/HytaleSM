import { Button } from '@/components/Button';

interface AuthPromptModalProps {
  isOpen: boolean;
  authUrl?: string;
  authCode?: string;
  onClose: () => void;
  onContinue?: () => void;
  title?: string;
}

export function AuthPromptModal({
  isOpen,
  authUrl,
  authCode,
  onClose,
  onContinue,
  title = 'Authentication Required',
}: AuthPromptModalProps) {
  if (!isOpen) {
    return null;
  }

  const authUrlWithCode = authCode
    ? `https://oauth.accounts.hytale.com/oauth2/device/verify?user_code=${encodeURIComponent(authCode)}`
    : undefined;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="absolute inset-0 bg-black/70" onClick={onClose} />
      <div className="relative z-10 w-full max-w-lg rounded-2xl border border-neutral-800 bg-neutral-950 p-6 shadow-2xl">
        <div className="mb-4">
          <h2 className="text-xl font-semibold text-white">{title}</h2>
          <p className="text-sm text-neutral-400 mt-1">
            Complete authentication in your browser, then return here. The download will resume automatically.
          </p>
        </div>
        <div className="space-y-4">
          <div className="rounded-lg border border-neutral-800 bg-neutral-900/60 p-4">
            <p className="text-xs uppercase text-neutral-500 mb-2">Authorization code</p>
            {authUrlWithCode ? (
              <a
                href={authUrlWithCode}
                target="_blank"
                rel="noreferrer"
                className="block text-center text-3xl font-semibold text-emerald-400 tracking-widest hover:text-emerald-300"
              >
                {authCode}
              </a>
            ) : (
              <div className="text-center text-3xl font-semibold text-emerald-400 tracking-widest">
                Waiting…
              </div>
            )}
          </div>
          <div className="rounded-lg border border-neutral-800 bg-neutral-900/60 p-4">
            <p className="text-xs uppercase text-neutral-500 mb-2">Verification link</p>
            <div className="space-y-2">
              {authUrlWithCode ? (
                <a
                  href={authUrlWithCode}
                  target="_blank"
                  rel="noreferrer"
                  className="flex items-center justify-center rounded-lg border border-emerald-500/40 bg-emerald-500/10 px-4 py-3 text-lg font-semibold text-emerald-300 hover:border-emerald-400 hover:text-emerald-200"
                >
                  Open verification page
                </a>
              ) : (
                <p className="text-sm text-neutral-400">Waiting for link…</p>
              )}
              <div className="text-xs text-neutral-500">
                via https://oauth.accounts.hytale.com/oauth2/device/verify
              </div>
            </div>
          </div>
        </div>
        <div className="mt-6 flex justify-end gap-3">
          {onContinue && (
            <Button variant="primary" onClick={onContinue}>Continue</Button>
          )}
          <Button variant="secondary" onClick={onClose}>Close</Button>
        </div>
      </div>
    </div>
  );
}
