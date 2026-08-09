import { useState, useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import {
  useEntityFields,
  useSetEntityField,
  friendlyErrorMessage,
  queryKeys,
  APIError,
  type EntityCustomField,
  type FieldEntityKind,
} from '../lib/api';
import { Input } from './ui/input';
import { DetailDivider } from './layout/DetailLayout';
import { cn } from '../lib/utils';

/**
 * saveErrorMessage picks the text a failed field save shows. The field-write
 * endpoint answers every refusal it can name with a sentence written for a
 * person -- archived (404), not attached here (404), bad value or required
 * (400) -- and its one unmapped arm answers a generic "operation failed",
 * never a raw internal string. So on this surface the server's own message is
 * always fit to show whenever the failure arrived as a structured envelope
 * (an APIError with a body). That is exactly the case friendlyErrorMessage
 * withholds for 404s elsewhere, where a 404 can be a bare "not found"; here it
 * is the honest "this field was archived..." the form must surface. A transport
 * or parse failure carries no such message and falls back.
 */
function saveErrorMessage(err: unknown): string {
  return err instanceof APIError && err.message ? err.message : 'Could not save.';
}

/**
 * CustomFieldsSection renders an entity's custom fields on the detail view —
 * the same section on Vector items and Beacon tickets, differing only in the
 * entity kind it addresses. Fields attached to this space's form are editable
 * inline (persisted through the field write path), with required attachments
 * marked on the label; legacy fields — values whose definition was archived,
 * removed, or detached from this form — are shown read-only so no data is
 * silently dropped.
 *
 * The section sits mid-rail, above the Created/Updated footer, and OWNS ITS
 * TRAILING DIVIDER: only the section knows whether it rendered anything, so a
 * divider managed by the page would either double up or dangle whenever the
 * form has no fields. With the divider in here, an empty section leaves the
 * rail exactly as it was before custom fields existed.
 */
export function CustomFieldsSection({
  spaceId,
  entityKind,
  entityId,
}: {
  spaceId: string;
  entityKind: FieldEntityKind;
  entityId: string;
}) {
  const { data: fields, isLoading, isError, error } = useEntityFields(spaceId, entityKind, entityId);

  if (isLoading) return null;
  // A failed fetch must not render as "this entity has no custom fields" —
  // this section is empty most of the time, so silence and failure would be
  // indistinguishable on a surface that carries required fields.
  if (isError) {
    return (
      <div data-testid="custom-fields-section">
        <h3 className="mb-2 text-[var(--text-xs)] font-semibold uppercase tracking-wide text-[var(--color-text-muted)]">
          Fields
        </h3>
        <p data-testid="custom-fields-error" className="text-[var(--text-xs)] text-[var(--color-danger)]">
          {friendlyErrorMessage(error, 'Custom fields could not be loaded.')}
        </p>
        <DetailDivider />
      </div>
    );
  }
  if (!fields || fields.length === 0) return null;

  return (
    <div data-testid="custom-fields-section">
      <h3 className="mb-2 text-[var(--text-xs)] font-semibold uppercase tracking-wide text-[var(--color-text-muted)]">
        Fields
      </h3>
      <div className="space-y-3">
        {fields.map((f) => (
          <CustomFieldRow key={f.slug} field={f} spaceId={spaceId} entityKind={entityKind} entityId={entityId} />
        ))}
      </div>
      <DetailDivider />
    </div>
  );
}

function CustomFieldRow({
  field,
  spaceId,
  entityKind,
  entityId,
}: {
  field: EntityCustomField;
  spaceId: string;
  entityKind: FieldEntityKind;
  entityId: string;
}) {
  const setMut = useSetEntityField(spaceId, entityKind, entityId);
  const queryClient = useQueryClient();
  const [value, setValue] = useState(field.value);
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Seed the value from the server, but never stomp an edit in progress —
  // BoardConfigSection's guard, on the same hazard. Every successful field save
  // invalidates the whole entity's field list, so a refetch lands on rows the
  // person may still be typing in; unguarded, it discarded what they had typed.
  //
  // The flag clears when the SERVER CATCHES UP rather than the moment a save
  // resolves, because useSetEntityField only invalidates the query — it does not
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
      {
        onError: (e) => {
          // Surface the server's own refusal, not a generic line — an archived,
          // detached or deleted definition names itself here.
          setError(saveErrorMessage(e));
          // Then refetch the form. A save can fail because the definition moved
          // out from under an open page (archived, detached, deleted); the
          // stale form still shows the editable, required-marked control the
          // write just refused. Invalidating re-renders the truth — the field
          // flips to its read-only legacy rendering within one round trip. This
          // is the general fix for every stale-form variant of the class, not
          // the archived one alone.
          queryClient.invalidateQueries({
            queryKey: queryKeys.entityFields(spaceId, entityKind, entityId),
          });
        },
      },
    );
  }

  const label = (
    <label
      htmlFor={`cf-${field.slug}`}
      className="mb-1 block text-[var(--text-xs)] text-[var(--color-text-muted)]"
    >
      {field.name}
      {field.required && !field.legacy && (
        // The server refuses clearing a required field; the marker says so
        // before the refusal has to.
        <span className="ml-0.5 text-[var(--color-danger)]" title="Required in this space">
          *
        </span>
      )}
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
        aria-required={field.required || undefined}
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
        aria-required={field.required || undefined}
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
