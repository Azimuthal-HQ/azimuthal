import { useEffect, useState } from 'react';
import { Check } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { Card, CardContent, CardHeader, CardTitle } from '../../components/ui/card';
import { useAuth } from '../../lib/auth';
import { friendlyErrorMessage, useOrganization, useUpdateOrganization } from '../../lib/api';
import { cn } from '../../lib/utils';

/**
 * OrgSettingsPage is the single home for organisation-level settings, inside
 * the admin panel (org admins only — the whole /admin area 404s non-admins).
 * It edits the org name and description; the slug is display-only because it
 * is not writable via any API and no user-facing URL uses it (routing is by
 * UUID). Boot-time config (allow_registration, invite_delivery) is server
 * environment only and deliberately not surfaced here as a toggle.
 */
export function OrgSettingsPage() {
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const { data: org } = useOrganization(orgId);
  const updateOrg = useUpdateOrganization(orgId);

  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [dirty, setDirty] = useState(false);
  const [saved, setSaved] = useState(false);

  // The same guard BoardConfigSection carries and for the same reason: this
  // form copies fetched data into editable state, so a refetch landing while
  // somebody is typing discarded what they had typed. Both fields go at once
  // here — the effect depends on the whole `org` object, so a change to either
  // one re-seeded both.
  //
  // The flag clears when the server catches up rather than when the save
  // resolves; useUpdateOrganization only invalidates, so for one render after a
  // successful save `org` still holds the pre-save values. See the fuller note
  // in CustomFieldsSection.
  useEffect(() => {
    if (!org) return;
    if (!dirty) {
      setName(org.name ?? '');
      setDescription(org.description ?? '');
    } else if ((org.name ?? '') === name && (org.description ?? '') === description) {
      setDirty(false);
    }
  }, [org, dirty, name, description]);

  async function handleSave() {
    if (!name.trim()) return;
    try {
      await updateOrg.mutateAsync({ name: name.trim(), description: description.trim() || undefined });
      setSaved(true);
      setTimeout(() => setSaved(false), 3000);
    } catch {
      // Surfaced through the mutation error below.
    }
  }

  return (
    <div className="max-w-2xl space-y-6" data-testid="admin-org-settings">
      <Card>
        <CardHeader>
          <CardTitle>Organization details</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-2">
            <label htmlFor="admin-org-name" className="block text-[var(--text-sm)] font-medium text-[var(--color-text)]">
              Name
            </label>
            <Input
              id="admin-org-name"
              data-testid="admin-org-name"
              value={name}
              onChange={(e) => { setName(e.target.value); setDirty(true); }}
            />
          </div>

          <div className="space-y-2">
            <label htmlFor="admin-org-slug" className="block text-[var(--text-sm)] font-medium text-[var(--color-text)]">
              Slug
            </label>
            <Input id="admin-org-slug" data-testid="admin-org-slug" value={org?.slug ?? ''} readOnly disabled />
            <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
              The workspace slug identifies your organization. It cannot be changed here — changing it would break
              existing links and bookmarks.
            </p>
          </div>

          <div className="space-y-2">
            <label htmlFor="admin-org-desc" className="block text-[var(--text-sm)] font-medium text-[var(--color-text)]">
              Description
            </label>
            <textarea
              id="admin-org-desc"
              data-testid="admin-org-description"
              rows={3}
              value={description}
              onChange={(e) => { setDescription(e.target.value); setDirty(true); }}
              className={cn(
                'flex w-full rounded-[var(--radius-lg)] border border-[var(--color-border)]',
                'bg-[var(--color-input)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)]',
                'placeholder:text-[var(--color-text-muted)]',
                'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
              )}
            />
          </div>

          {updateOrg.error && (
            <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
              {friendlyErrorMessage(updateOrg.error, 'The organization could not be saved.')}
            </p>
          )}
          {saved && (
            <div className="flex items-center gap-2 text-[var(--text-sm)] text-[var(--color-success)]">
              <Check className="h-4 w-4" />
              Changes saved successfully
            </div>
          )}
          <div className="flex justify-end">
            <Button
              data-testid="admin-org-save"
              onClick={handleSave}
              disabled={updateOrg.isPending || !name.trim()}
            >
              {updateOrg.isPending ? 'Saving...' : 'Save Changes'}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
