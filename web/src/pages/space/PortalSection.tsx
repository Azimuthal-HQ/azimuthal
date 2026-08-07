import { useState } from 'react';
import { Copy } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '../../components/ui/card';
import { Input } from '../../components/ui/input';
import { Field, FieldLabel } from '../../components/ui/field';
import { cn } from '../../lib/utils';
import {
  friendlyErrorMessage,
  useCreatePortal,
  usePortalConfig,
  useUpdatePortal,
} from '../../lib/api';

const textareaClass = cn(
  'w-full resize-none rounded-[var(--radius-lg)] border border-[var(--color-border)]',
  'bg-[var(--color-input)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)]',
  'placeholder:text-[var(--color-text-muted)]',
  'focus:outline-none focus:ring-1 focus:border-[var(--color-primary)] focus:ring-[var(--color-primary)]',
);

/**
 * PortalSection is the agent-facing half of the customer portal: create one,
 * read the URL to hand out, rename it, and turn it on and off.
 *
 * Three states, mirroring BoardConfigSection's shape: no portal yet (the
 * config GET 404s — that is the "offer to create one" signal, not an error),
 * portal configured, and forbidden (403 — manage_space is checked in the
 * handler, so a member without it sees this line and never a raw error).
 *
 * One deliberate divergence from BoardConfigSection's interior: the editable
 * name/intro pair is seeded by FALLBACK (`draft ?? server`) rather than by
 * BoardConfig's setState-in-an-effect, which survives there only under a
 * lint exemption contracted to that legacy list — a new surface must be
 * eslint-clean. The observable contract is identical: the server value shows
 * until the first keystroke, edits win over refetches, and a save returns
 * the fields to server truth.
 *
 * The toggle sends `enabled` ALONE and the save sends only name and intro:
 * the PATCH is three-state per field (absent = leave alone), so the toggle
 * cannot clobber an in-progress rename and a rename cannot flip the switch.
 */
export function PortalSection({ orgId, spaceId }: { orgId: string; spaceId: string }) {
  const configQuery = usePortalConfig(orgId, spaceId);
  const createMutation = useCreatePortal(orgId, spaceId);
  const updateMutation = useUpdatePortal(orgId, spaceId);

  // The create form (no portal yet).
  const [createName, setCreateName] = useState('');
  const [createIntro, setCreateIntro] = useState('');

  // The edit form (portal exists). null = not edited, show the server value.
  const [draftName, setDraftName] = useState<string | null>(null);
  const [draftIntro, setDraftIntro] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);

  const forbidden = configQuery.error?.status === 403;
  const noPortal = configQuery.error?.status === 404;
  const cfg = configQuery.data;

  const dirty = draftName !== null || draftIntro !== null;
  const nameValue = draftName ?? cfg?.name ?? '';
  const introValue = draftIntro ?? cfg?.intro ?? '';

  // The full URL a customer uses, not the bare key: the key means nothing
  // without the /portal/ path, and this string is exactly what an agent
  // pastes into an email or a company wiki.
  const portalUrl = cfg ? `${window.location.origin}/portal/${cfg.portal_key}` : '';

  async function handleCreate() {
    setError(null);
    try {
      const intro = createIntro.trim();
      await createMutation.mutateAsync({
        name: createName.trim(),
        ...(intro === '' ? {} : { intro }),
      });
    } catch (e) {
      setError(friendlyErrorMessage(e, 'The portal could not be created.'));
    }
  }

  async function handleSave() {
    setError(null);
    try {
      await updateMutation.mutateAsync({ name: nameValue.trim(), intro: introValue.trim() });
      setDraftName(null);
      setDraftIntro(null);
    } catch (e) {
      setError(friendlyErrorMessage(e, 'The portal could not be updated.'));
    }
  }

  async function handleToggle() {
    if (!cfg) return;
    setError(null);
    try {
      await updateMutation.mutateAsync({ enabled: !cfg.enabled });
    } catch (e) {
      setError(friendlyErrorMessage(e, 'The portal could not be updated.'));
    }
  }

  // "Copied" only when the write actually happened. navigator.clipboard is
  // undefined on non-secure contexts — and plain-http self-hosting is exactly
  // this project's audience — so the old optional chain no-oped and the
  // button lied. The write can also reject (permissions policy). Either way
  // the honest fallback is legible: the URL sits selectable beside the
  // button, and the failure line says to use it.
  async function handleCopy() {
    setCopyFailed(false);
    try {
      if (!navigator.clipboard) {
        throw new Error('clipboard unavailable');
      }
      await navigator.clipboard.writeText(portalUrl);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopyFailed(true);
    }
  }

  return (
    <Card data-testid="portal-section">
      <CardHeader>
        <CardTitle>Customer portal</CardTitle>
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
          A public page where external customers sign in by email link and raise requests
          against this service desk — no account, and no view of anything but their own
          requests.
        </p>
      </CardHeader>
      <CardContent>
        {forbidden ? (
          <p data-testid="portal-forbidden" className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
            You need space admin to manage the customer portal.
          </p>
        ) : configQuery.isLoading ? (
          <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading…</p>
        ) : noPortal ? (
          <div className="space-y-3" data-testid="portal-create">
            <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
              This space has no portal yet. Creating one gives customers a sign-in page at a
              permanent URL you can hand out.
            </p>
            <Field>
              <FieldLabel htmlFor="portal-create-name">Portal name</FieldLabel>
              <Input
                id="portal-create-name"
                data-testid="portal-create-name"
                placeholder="Acme Support"
                value={createName}
                onChange={(e) => setCreateName(e.target.value)}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="portal-create-intro" optional>
                Introduction
              </FieldLabel>
              <textarea
                id="portal-create-intro"
                data-testid="portal-create-intro"
                rows={2}
                placeholder="Tell us what you need and we will pick it up."
                value={createIntro}
                onChange={(e) => setCreateIntro(e.target.value)}
                className={textareaClass}
              />
            </Field>
            <Button
              data-testid="portal-create-button"
              disabled={createMutation.isPending || createName.trim() === ''}
              onClick={handleCreate}
            >
              {createMutation.isPending ? 'Creating…' : 'Create portal'}
            </Button>
            {error && <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{error}</p>}
          </div>
        ) : configQuery.error ? (
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {friendlyErrorMessage(configQuery.error, 'The portal configuration could not be loaded.')}
          </p>
        ) : cfg ? (
          <div className="space-y-4" data-testid="portal-configured">
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-[var(--radius-lg)] border border-[var(--color-border)] p-3">
              <div className="min-w-0">
                <p
                  data-testid="portal-config-state"
                  className="text-[var(--text-sm)] font-medium text-[var(--color-text)]"
                >
                  {cfg.enabled ? 'The portal is live' : 'The portal is disabled'}
                </p>
                <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  {cfg.enabled
                    ? 'Customers with the link below can sign in and raise requests.'
                    : 'Customers get a not-found page. Re-enabling keeps the same URL, so links already handed out work again.'}
                </p>
              </div>
              <Button
                variant="outline"
                size="sm"
                data-testid="portal-config-toggle"
                disabled={updateMutation.isPending}
                onClick={handleToggle}
              >
                {cfg.enabled ? 'Disable' : 'Enable'}
              </Button>
            </div>

            <div>
              <p className="mb-1 text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                Customer URL
              </p>
              <div className="flex items-center gap-2">
                <code
                  data-testid="portal-config-url"
                  title={portalUrl}
                  className="min-w-0 flex-1 truncate rounded-[var(--radius-md)] bg-[var(--color-surface-hover)] px-2 py-1.5 font-mono text-[var(--text-xs)] text-[var(--color-text)]"
                >
                  {portalUrl}
                </code>
                <Button
                  variant="outline"
                  size="sm"
                  data-testid="portal-config-copy"
                  onClick={() => void handleCopy()}
                >
                  <Copy className="mr-1 h-3.5 w-3.5" />
                  {copied ? 'Copied' : 'Copy link'}
                </Button>
              </div>
              {copyFailed && (
                <p data-testid="portal-copy-failed" className="mt-1 text-[var(--text-xs)] text-[var(--color-danger)]">
                  Copying is not available here — select the URL above and copy it yourself.
                </p>
              )}
              <p className="mt-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                The key <code data-testid="portal-config-key">{cfg.portal_key}</code> is permanent —
                renaming or disabling the portal never changes it.
              </p>
            </div>

            <Field>
              <FieldLabel htmlFor="portal-config-name">Portal name</FieldLabel>
              <Input
                id="portal-config-name"
                data-testid="portal-config-name"
                value={nameValue}
                onChange={(e) => {
                  setDraftName(e.target.value);
                  setError(null);
                }}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="portal-config-intro" optional>
                Introduction
              </FieldLabel>
              <textarea
                id="portal-config-intro"
                data-testid="portal-config-intro"
                rows={2}
                value={introValue}
                onChange={(e) => {
                  setDraftIntro(e.target.value);
                  setError(null);
                }}
                className={textareaClass}
              />
            </Field>
            <Button
              size="sm"
              data-testid="portal-config-save"
              disabled={updateMutation.isPending || !dirty || nameValue.trim() === ''}
              onClick={handleSave}
            >
              {updateMutation.isPending ? 'Saving…' : 'Save changes'}
            </Button>
            {error && <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{error}</p>}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}
