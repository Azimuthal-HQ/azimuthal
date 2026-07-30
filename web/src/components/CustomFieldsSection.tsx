import { useState, useEffect } from 'react';
import { useItemFields, useSetItemField, friendlyErrorMessage, type ItemCustomField } from '../lib/api';
import { Input } from './ui/input';
import { cn } from '../lib/utils';

/**
 * CustomFieldsSection renders an item's custom fields on the detail view: active
 * definitions are editable inline (persisted through the field write path);
 * legacy fields — values whose definition was archived or removed — are shown
 * read-only so no data is silently dropped.
 */
export function CustomFieldsSection({ spaceId, itemId }: { spaceId: string; itemId: string }) {
  const { data: fields, isLoading } = useItemFields(spaceId, itemId);

  if (isLoading || !fields || fields.length === 0) return null;

  return (
    <div data-testid="custom-fields-section">
      <h3 className="mb-2 text-[var(--text-xs)] font-semibold uppercase tracking-wide text-[var(--color-text-muted)]">
        Fields
      </h3>
      <div className="space-y-3">
        {fields.map((f) => (
          <CustomFieldRow key={f.slug} field={f} spaceId={spaceId} itemId={itemId} />
        ))}
      </div>
    </div>
  );
}

function CustomFieldRow({ field, spaceId, itemId }: { field: ItemCustomField; spaceId: string; itemId: string }) {
  const setMut = useSetItemField(spaceId, itemId);
  const [value, setValue] = useState(field.value);
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Seed the value from the server, but never stomp an edit in progress —
  // BoardConfigSection's guard, on the same hazard. Every successful field save
  // invalidates the whole item's field list, so a refetch lands on rows the
  // person may still be typing in; unguarded, it discarded what they had typed.
  //
  // The flag clears when the SERVER CATCHES UP rather than the moment a save
  // resolves, because useSetItemField only invalidates the query — it does not
  // write the new value through the cache the way the board mutations do. For
  // one render after a successful save field.value is still the pre-save value,
  // and clearing on success would flash that old text back into the input the
  // person had just left. Once the server agrees with what is on screen there
  // is nothing left to protect and the row follows refetches again, which also
  // covers typing a change and then typing it back.
  useEffect(() => {
    if (!dirty) {
      setValue(field.value);
    } else if (field.value === value) {
      setDirty(false);
    }
  }, [field.value, dirty, value]);

  // Every local edit goes through here, so there is one place the flag is set
  // rather than one per control.
  function edit(next: string) {
    setValue(next);
    setDirty(true);
  }

  function persist(next: string) {
    if (next === field.value) return;
    setError(null);
    setMut.mutate(
      { slug: field.slug, value: next },
      { onError: (e) => setError(friendlyErrorMessage(e, 'Could not save.')) },
    );
  }

  const label = (
    <label
      htmlFor={`cf-${field.slug}`}
      className="mb-1 block text-[var(--text-xs)] text-[var(--color-text-muted)]"
    >
      {field.name}
      {field.legacy && (
        <span className="ml-1.5 rounded-[4px] bg-[var(--color-surface-2)] px-1.5 py-0.5 text-[10px] text-[var(--color-text-muted)]">
          legacy
        </span>
      )}
    </label>
  );

  // Legacy fields are read-only — display the stored value, never editable.
  if (field.legacy) {
    return (
      <div>
        {label}
        <p className="text-[var(--text-sm)] text-[var(--color-text)]">{field.value || '—'}</p>
      </div>
    );
  }

  let control;
  if (field.field_type === 'single_select') {
    control = (
      <select
        id={`cf-${field.slug}`}
        value={value}
        onChange={(e) => {
          edit(e.target.value);
          persist(e.target.value);
        }}
        className={cn(
          'flex h-9 w-full rounded-[var(--radius-lg)] border border-[var(--color-border)]',
          'bg-[var(--color-input)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
          'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
        )}
      >
        <option value="">—</option>
        {field.options.map((o) => (
          <option key={o} value={o}>{o}</option>
        ))}
      </select>
    );
  } else {
    const inputType = field.field_type === 'number' ? 'number' : field.field_type === 'date' ? 'date' : 'text';
    control = (
      <Input
        id={`cf-${field.slug}`}
        type={inputType}
        value={value}
        onChange={(e) => edit(e.target.value)}
        onBlur={() => persist(value)}
      />
    );
  }

  return (
    <div>
      {label}
      {control}
      {error && <p className="mt-1 text-[var(--text-xs)] text-[var(--color-danger)]">{error}</p>}
    </div>
  );
}
