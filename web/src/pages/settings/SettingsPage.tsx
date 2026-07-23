import { useRef, useState, type ChangeEvent } from 'react';
import { Shield, Palette, User, Check } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { Card, CardContent, CardHeader, CardTitle } from '../../components/ui/card';
import { SegmentedControl } from '../../components/ui/segmented';
import { useTheme } from '../../components/theme/ThemeProvider';
import { useAuth } from '../../lib/auth';
import { friendlyErrorMessage, useUpdateProfile, useUploadOwnAvatar } from '../../lib/api';
import { cn } from '../../lib/utils';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type TabId = 'profile' | 'appearance';

interface TabDef {
  id: TabId;
  label: string;
  icon: typeof User;
}

// Organization settings moved to the admin panel (/admin/settings) — this
// page holds only the user's own Profile and Appearance.
const TABS: TabDef[] = [
  { id: 'profile', label: 'Profile', icon: User },
  { id: 'appearance', label: 'Appearance', icon: Palette },
];

const FONT_SIZE_OPTIONS: { value: 'sm' | 'base' | 'lg'; label: string }[] = [
  { value: 'sm', label: 'Small' },
  { value: 'base', label: 'Default' },
  { value: 'lg', label: 'Large' },
];

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

/** Settings page with Profile, Organization, and Appearance tabs. */
export function SettingsPage() {
  const [activeTab, setActiveTab] = useState<TabId>('profile');
  const { user } = useAuth();

  // Profile state
  const [displayName, setDisplayName] = useState(user?.email?.split('@')[0] ?? '');
  const [email, setEmail] = useState(user?.email ?? '');
  const [profileSaveSuccess, setProfileSaveSuccess] = useState(false);
  const updateProfileMutation = useUpdateProfile();

  // Self avatar upload.
  const uploadAvatar = useUploadOwnAvatar();
  const [avatarUrl, setAvatarUrl] = useState<string | null>(null);
  const avatarFileRef = useRef<HTMLInputElement>(null);

  const onPickAvatar = (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    uploadAvatar.mutate(file, { onSuccess: (res) => setAvatarUrl(res.avatar_url) });
  };

  async function handleSaveProfile() {
    try {
      await updateProfileMutation.mutateAsync({ display_name: displayName.trim(), email: email.trim() });
      setProfileSaveSuccess(true);
      setTimeout(() => setProfileSaveSuccess(false), 3000);
    } catch {
      // error handled by mutation state
    }
  }

  // Appearance state
  const { theme, setTheme } = useTheme();
  const [fontSize, setFontSize] = useState<'sm' | 'base' | 'lg'>('base');

  const initials = displayName
    ? displayName.slice(0, 2).toUpperCase()
    : (user?.email?.slice(0, 2).toUpperCase() ?? '??');

  return (
    <div className="space-y-6">
      <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">
        Settings
      </h1>

      {/* Tabs */}
      <div className="flex gap-1 border-b border-[var(--color-border)]">
        {TABS.map((tab) => {
          const Icon = tab.icon;
          const isActive = activeTab === tab.id;
          return (
            <button
              key={tab.id}
              type="button"
              onClick={() => setActiveTab(tab.id)}
              className={cn(
                'flex items-center gap-2 border-b-2 px-4 py-2.5 text-[var(--text-sm)] font-medium transition-colors',
                isActive
                  ? 'border-[var(--color-primary)] text-[var(--color-primary)]'
                  : 'border-transparent text-[var(--color-text-muted)] hover:text-[var(--color-text)]',
              )}
            >
              <Icon className="h-4 w-4" />
              {tab.label}
            </button>
          );
        })}
      </div>

      {/* Tab content */}
      <div className="max-w-2xl">
        {activeTab === 'profile' && (
          <div className="space-y-6">
            {/* Avatar */}
            <Card>
              <CardHeader>
                <CardTitle>Avatar</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="flex items-center gap-4">
                  {avatarUrl ? (
                    <img
                      src={avatarUrl}
                      alt="Your avatar"
                      data-testid="settings-avatar-image"
                      className="h-16 w-16 rounded-full object-cover"
                    />
                  ) : (
                    <div
                      className={cn(
                        'flex h-16 w-16 items-center justify-center rounded-full',
                        'bg-[var(--color-primary-muted)] text-[var(--text-xl)] font-semibold text-[var(--color-primary)]',
                      )}
                    >
                      {initials}
                    </div>
                  )}
                  <div className="space-y-1">
                    <Button
                      variant="outline"
                      size="sm"
                      data-testid="settings-avatar-upload"
                      disabled={uploadAvatar.isPending}
                      onClick={() => avatarFileRef.current?.click()}
                    >
                      {uploadAvatar.isPending ? 'Uploading...' : 'Change avatar'}
                    </Button>
                    <input
                      ref={avatarFileRef}
                      type="file"
                      accept="image/png,image/jpeg,image/webp,image/gif"
                      className="hidden"
                      data-testid="settings-avatar-input"
                      onChange={onPickAvatar}
                    />
                    {uploadAvatar.error && (
                      <p className="text-[var(--text-xs)] text-[var(--color-danger)]">
                        {friendlyErrorMessage(uploadAvatar.error, 'The avatar could not be uploaded.')}
                      </p>
                    )}
                  </div>
                </div>
              </CardContent>
            </Card>

            {/* Profile fields */}
            <Card>
              <CardHeader>
                <CardTitle>Profile Information</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="space-y-2">
                  <label
                    htmlFor="displayName"
                    className="block text-[var(--text-sm)] font-medium text-[var(--color-text)]"
                  >
                    Display Name
                  </label>
                  <Input
                    id="displayName"
                    value={displayName}
                    onChange={(e) => setDisplayName(e.target.value)}
                  />
                </div>
                <div className="space-y-2">
                  <label
                    htmlFor="email"
                    className="block text-[var(--text-sm)] font-medium text-[var(--color-text)]"
                  >
                    Email
                  </label>
                  <Input
                    id="email"
                    type="email"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    disabled
                  />
                </div>
                <div className="flex items-center justify-end gap-3">
                  {profileSaveSuccess && (
                    <span className="flex items-center gap-1 text-[var(--text-sm)] text-[var(--color-success)]">
                      <Check className="h-4 w-4" />Saved
                    </span>
                  )}
                  {updateProfileMutation.error && (
                    <span className="text-[var(--text-sm)] text-[var(--color-danger)]">
                      {friendlyErrorMessage(updateProfileMutation.error, 'Your profile could not be saved.')}
                    </span>
                  )}
                  <Button onClick={handleSaveProfile} disabled={updateProfileMutation.isPending}>
                    {updateProfileMutation.isPending ? 'Saving...' : 'Save Changes'}
                  </Button>
                </div>
              </CardContent>
            </Card>
          </div>
        )}

        {activeTab === 'appearance' && (
          <div className="space-y-6">
            {/* Theme */}
            <Card>
              <CardHeader>
                <CardTitle>Theme</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="flex gap-3">
                  <button
                    type="button"
                    onClick={() => setTheme('light')}
                    className={cn(
                      'flex flex-col items-center gap-2 rounded-[var(--radius-lg)] border-2 p-4 transition-colors',
                      theme === 'light'
                        ? 'border-[var(--color-primary)] bg-[var(--color-primary-muted)]'
                        : 'border-[var(--color-border)] hover:border-[var(--color-text-muted)]',
                    )}
                  >
                    <div className="flex h-10 w-16 items-center justify-center rounded-[var(--radius-md)] border border-gray-200 bg-white">
                      <span className="text-[var(--text-xs)] text-gray-800">Aa</span>
                    </div>
                    <span className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                      Light
                    </span>
                  </button>

                  <button
                    type="button"
                    onClick={() => setTheme('dark')}
                    className={cn(
                      'flex flex-col items-center gap-2 rounded-[var(--radius-lg)] border-2 p-4 transition-colors',
                      theme === 'dark'
                        ? 'border-[var(--color-primary)] bg-[var(--color-primary-muted)]'
                        : 'border-[var(--color-border)] hover:border-[var(--color-text-muted)]',
                    )}
                  >
                    <div className="flex h-10 w-16 items-center justify-center rounded-[var(--radius-md)] border border-gray-700 bg-gray-900">
                      <span className="text-[var(--text-xs)] text-gray-200">Aa</span>
                    </div>
                    <span className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                      Dark
                    </span>
                  </button>
                </div>
              </CardContent>
            </Card>

            {/* Font size */}
            <Card>
              <CardHeader>
                <CardTitle>Font Size</CardTitle>
              </CardHeader>
              <CardContent>
                <SegmentedControl
                  options={FONT_SIZE_OPTIONS}
                  value={fontSize}
                  onChange={setFontSize}
                  aria-label="Font size"
                  fullWidth={false}
                />
              </CardContent>
            </Card>

            {/* Security info */}
            <Card>
              <CardHeader>
                <div className="flex items-center gap-2">
                  <Shield className="h-5 w-5 text-[var(--color-text-muted)]" />
                  <CardTitle>Security</CardTitle>
                </div>
              </CardHeader>
              <CardContent>
                <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
                  All sessions are encrypted. Your password is hashed using bcrypt and never stored in plain text.
                </p>
              </CardContent>
            </Card>
          </div>
        )}
      </div>
    </div>
  );
}
