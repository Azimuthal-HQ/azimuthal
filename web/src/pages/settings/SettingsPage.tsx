import { useState } from 'react';
import { Shield, Palette, User, Building2 } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { Card, CardContent, CardHeader, CardTitle } from '../../components/ui/card';
import { useTheme } from '../../components/theme/ThemeProvider';
import { useAuth } from '../../lib/auth';
import { cn } from '../../lib/utils';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type TabId = 'profile' | 'organization' | 'appearance';

interface TabDef {
  id: TabId;
  label: string;
  icon: typeof User;
}

const TABS: TabDef[] = [
  { id: 'profile', label: 'Profile', icon: User },
  { id: 'organization', label: 'Organization', icon: Building2 },
  { id: 'appearance', label: 'Appearance', icon: Palette },
];

const FONT_SIZE_OPTIONS = [
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

  // Organization state
  const [orgName, setOrgName] = useState('');
  const [orgSlug, setOrgSlug] = useState('');
  const [orgDescription, setOrgDescription] = useState('');

  // Appearance state
  const { theme, setTheme } = useTheme();
  const [fontSize, setFontSize] = useState('base');

  const initials = displayName
    ? displayName.slice(0, 2).toUpperCase()
    : (user?.email?.slice(0, 2).toUpperCase() ?? '??');

  return (
    <div className="space-y-6">
      <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
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
                  <div
                    className={cn(
                      'flex h-16 w-16 items-center justify-center rounded-full',
                      'bg-[var(--color-primary-muted)] text-[var(--text-xl)] font-semibold text-[var(--color-primary)]',
                    )}
                  >
                    {initials}
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
                <div className="flex justify-end">
                  <Button>Save Changes</Button>
                </div>
              </CardContent>
            </Card>
          </div>
        )}

        {activeTab === 'organization' && (
          <div className="space-y-6">
            <Card>
              <CardHeader>
                <CardTitle>Organization Details</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="space-y-2">
                  <label
                    htmlFor="orgName"
                    className="block text-[var(--text-sm)] font-medium text-[var(--color-text)]"
                  >
                    Name
                  </label>
                  <Input
                    id="orgName"
                    value={orgName}
                    onChange={(e) => setOrgName(e.target.value)}
                  />
                </div>
                <div className="space-y-2">
                  <label
                    htmlFor="orgSlug"
                    className="block text-[var(--text-sm)] font-medium text-[var(--color-text)]"
                  >
                    Slug
                  </label>
                  <Input
                    id="orgSlug"
                    value={orgSlug}
                    onChange={(e) => setOrgSlug(e.target.value)}
                  />
                </div>
                <div className="space-y-2">
                  <label
                    htmlFor="orgDesc"
                    className="block text-[var(--text-sm)] font-medium text-[var(--color-text)]"
                  >
                    Description
                  </label>
                  <textarea
                    id="orgDesc"
                    rows={3}
                    value={orgDescription}
                    onChange={(e) => setOrgDescription(e.target.value)}
                    className={cn(
                      'flex w-full rounded-[var(--radius-md)] border border-[var(--color-border)]',
                      'bg-[var(--color-surface)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)]',
                      'shadow-[var(--shadow-sm)] placeholder:text-[var(--color-text-muted)]',
                      'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]',
                    )}
                  />
                </div>
                <div className="flex justify-end">
                  <Button>Save Changes</Button>
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
                <div className="flex gap-2">
                  {FONT_SIZE_OPTIONS.map((option) => (
                    <button
                      key={option.value}
                      type="button"
                      onClick={() => setFontSize(option.value)}
                      className={cn(
                        'rounded-[var(--radius-md)] border px-4 py-2 text-[var(--text-sm)] font-medium transition-colors',
                        fontSize === option.value
                          ? 'border-[var(--color-primary)] bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
                          : 'border-[var(--color-border)] text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]',
                      )}
                    >
                      {option.label}
                    </button>
                  ))}
                </div>
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
