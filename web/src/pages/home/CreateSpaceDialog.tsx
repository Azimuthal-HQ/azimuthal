import { useState } from 'react';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { FieldLabel } from '../../components/ui/field';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from '../../components/ui/dialog';
import { cn } from '../../lib/utils';
import { friendlyErrorMessage, useCreateSpace, type Space, type SpaceType } from '../../lib/api';
import { MODULE_KEYS, MODULES, spacePath } from '../../shell/modules';

interface CreateSpaceDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  orgId: string;
  /** Called with the path of the space that was created. */
  onCreated: (path: string) => void;
}

function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '');
}

function deriveKey(name: string): string {
  const first = name.trim().split(/\s+/)[0] ?? '';
  const upper = first.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 8);
  return upper || 'SPACE';
}

function linkForSpace(space: Space): string {
  return spacePath(space.type, space.id, MODULES[space.type].defaultSubpath);
}

/**
 * Create a space.
 *
 * Extracted from the interim Home page unchanged, because P5 replaces that
 * page with a dashboard and the dialog has to survive the replacement: the top
 * bar's Create control navigates to `/?create=space` whenever the reader is
 * not inside a space, and losing this would make that button silently do
 * nothing. It lives in its own file rather than inside the new Home so the
 * page that hosts it can change again without moving it a second time.
 */
export function CreateSpaceDialog({
  open,
  onOpenChange,
  orgId,
  onCreated,
}: CreateSpaceDialogProps) {
  const createSpaceMutation = useCreateSpace(orgId);
  const [formName, setFormName] = useState('');
  const [formType, setFormType] = useState<SpaceType>('beacon');
  const [formDescription, setFormDescription] = useState('');
  const [formKey, setFormKey] = useState('');

  function resetForm() {
    setFormName('');
    setFormType('beacon');
    setFormDescription('');
    setFormKey('');
  }

  const keyError =
    formType === 'beacon' && formKey && !/^[A-Z0-9]{1,10}$/.test(formKey)
      ? 'Abbreviation must be 1–10 uppercase letters or digits'
      : null;
  const keyMissing = formType === 'beacon' && !formKey.trim();

  async function handleCreate() {
    const name = formName.trim();
    if (!name || keyError || keyMissing) return;
    try {
      const created = await createSpaceMutation.mutateAsync({
        name,
        slug: slugify(name),
        key: formType === 'beacon' ? formKey : deriveKey(name),
        type: formType,
        description: formDescription.trim() || undefined,
      });
      onOpenChange(false);
      resetForm();
      onCreated(linkForSpace(created));
    } catch {
      // Rendered from the mutation's error state below.
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        onOpenChange(next);
        if (!next) resetForm();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create a new space</DialogTitle>
          <DialogDescription>
            Spaces are where your team organises work. Choose a type to get started.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div>
            <FieldLabel htmlFor="space-name">Name</FieldLabel>
            <Input
              id="space-name"
              placeholder="e.g. HR Support"
              value={formName}
              onChange={(e) => setFormName(e.target.value)}
              autoFocus
            />
          </div>

          <div>
            <FieldLabel id="space-type-label">Type</FieldLabel>
            <div className="grid grid-cols-3 gap-2">
              {MODULE_KEYS.map((key) => {
                const def = MODULES[key];
                return (
                  <button
                    key={key}
                    type="button"
                    onClick={() => setFormType(key)}
                    className={cn(
                      'flex flex-col items-center gap-2 rounded-[var(--radius-lg)] border p-3 transition-colors',
                      formType === key
                        ? 'border-[var(--color-primary)] bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
                        : 'border-[var(--color-border)] text-[var(--color-text-muted)] hover:border-[var(--color-text-muted)] hover:text-[var(--color-text)]',
                    )}
                  >
                    <def.icon className="h-5 w-5" />
                    <span className="text-[var(--text-xs)] font-medium">{def.name}</span>
                  </button>
                );
              })}
            </div>
          </div>

          {formType === 'beacon' && (
            <div className="space-y-2">
              <FieldLabel htmlFor="space-key">Ticket abbreviation</FieldLabel>
              <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
                A short prefix used to number tickets — e.g. <span className="font-mono">HR</span> →{' '}
                <span className="font-mono">HR-1</span>, <span className="font-mono">HR-2</span>
              </p>
              <Input
                id="space-key"
                placeholder="e.g. HR, SUPPORT, IT"
                value={formKey}
                onChange={(e) =>
                  setFormKey(e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 10))
                }
                className="w-40 font-mono"
              />
              {keyError && (
                <p className="text-[var(--text-xs)] text-[var(--color-danger)]">{keyError}</p>
              )}
            </div>
          )}

          <div>
            <FieldLabel htmlFor="space-desc" optional>
              Description
            </FieldLabel>
            <Input
              id="space-desc"
              placeholder="What is this space for?"
              value={formDescription}
              onChange={(e) => setFormDescription(e.target.value)}
            />
          </div>

          {/* friendlyErrorMessage passes human-written conflict and validation
              messages through (e.g. a slug taken in this module) and collapses
              everything else to the fallback (P2.5 W5). */}
          {createSpaceMutation.error && (
            <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
              {friendlyErrorMessage(createSpaceMutation.error, 'The space could not be created.')}
            </p>
          )}
        </div>

        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" type="button">
              Cancel
            </Button>
          </DialogClose>
          <Button
            onClick={handleCreate}
            disabled={
              createSpaceMutation.isPending || !formName.trim() || !!keyMissing || !!keyError
            }
          >
            {createSpaceMutation.isPending ? 'Creating...' : 'Create Space'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
