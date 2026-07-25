import {
  useEffect,
  useId,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
} from 'react';
import { cn } from '../lib/utils';
import { useTicketRefSuggestions, type TicketRefSuggestion } from '../lib/api';
import { Field, FieldHint, FieldLabel } from './ui/field';
import { Input } from './ui/input';

export interface TicketRefFieldProps {
  /** Org whose tickets the typeahead searches; '' disables the query. */
  orgId: string;
  /** The ticket_ref text, controlled by the caller. Always a plain string. */
  value: string;
  onChange: (value: string) => void;
  label?: string;
  /** Hint under the input; callers usually name where the reference lands. */
  hint?: ReactNode;
  placeholder?: string;
  /**
   * Marks the field required in the UI. Nothing sets it today — the mandate
   * lives in the deployment's AZIMUTHAL_TICKET_REF_REQUIRED, which no API
   * response exposes to the client yet. It exists so the surface can say so
   * once one does; it does NOT make the component validate anything.
   */
  required?: boolean;
  disabled?: boolean;
  /** data-testid of the input; every inner id derives from it. */
  testId?: string;
  className?: string;
  /** Server cap is 200 characters (internal/core/api/ticketref). */
  maxLength?: number;
}

/**
 * TicketRefField (A1): the ticket_ref input, with a typeahead over the org's
 * tickets.
 *
 * **Free text is fully valid and always wins.** ticket_ref is an operator's
 * note to the audit log, not a foreign key — it may name a ticket in a tracker
 * Azimuthal has never heard of, a change-management record, or nothing at all.
 * So this component never blocks submission on "not a known ticket", never
 * rewrites what was typed, never shows a validation error for an unmatched
 * value, and never auto-selects the first suggestion. Choosing a suggestion is
 * a shortcut that fills the input with its `ref`; typing straight past the list
 * is the normal path.
 *
 * It follows PersonTeamPicker for the dropdown mechanics — click-outside
 * closing, option markup, empty and loading rows, the `${testId}-…` id scheme
 * — with one deliberate difference nobody should "fix": PersonTeamPicker
 * resolves to a typed *selection* and swaps its input for a chip once chosen,
 * because a grant subject must be a real user or team. This field stays a
 * free-text input at every moment of its life. If you find yourself adding a
 * chip, a "clear selection" button, a `PickedSubject`-shaped value, or a
 * "please choose a ticket" error, you are turning an assistive typeahead into
 * a constraint the server does not have.
 *
 * Escape closes the suggestion list. Inside a Radix dialog it also closes the
 * dialog: Radix listens for Escape with a document capture listener, which
 * runs before any React handler here can stop it. That is the shipped
 * behaviour of a plain input in these dialogs too, so it is left alone rather
 * than worked around.
 */
export function TicketRefField({
  orgId,
  value,
  onChange,
  label = 'Ticket reference',
  hint,
  placeholder = 'e.g. BEA-42',
  required = false,
  disabled = false,
  testId = 'ticket-ref',
  className,
  maxLength = 200,
}: TicketRefFieldProps) {
  const [open, setOpen] = useState(false);
  // -1 = "the text as typed"; Enter only accepts a suggestion above that.
  const [active, setActive] = useState(-1);
  const rootRef = useRef<HTMLDivElement>(null);
  const reactId = useId();
  const inputId = `${testId}-${reactId}`;
  const listId = `${testId}-suggestions`;

  const q = value.trim();
  // The query is disabled until the field is in use, so a dialog that merely
  // renders the field never asks the server for suggestions.
  const suggestions = useTicketRefSuggestions(open ? orgId : '', q);

  useEffect(() => {
    function onClickOutside(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener('mousedown', onClickOutside);
    return () => document.removeEventListener('mousedown', onClickOutside);
  }, []);

  const options: TicketRefSuggestion[] = open && q ? (suggestions.data ?? []) : [];
  const searching = open && q.length > 0 && suggestions.isLoading;
  const empty = open && q.length > 0 && !searching && options.length === 0;
  const listVisible = open && q.length > 0;

  const accept = (s: TicketRefSuggestion) => {
    onChange(s.ref);
    setOpen(false);
    setActive(-1);
  };

  const onKeyDown = (e: ReactKeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Escape') {
      setOpen(false);
      setActive(-1);
      return;
    }
    if (e.key === 'Tab') {
      setOpen(false);
      setActive(-1);
      return;
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (!open) {
        setOpen(true);
        return;
      }
      setActive((i) => (options.length === 0 ? -1 : Math.min(i + 1, options.length - 1)));
      return;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive((i) => Math.max(i - 1, -1));
      return;
    }
    if (e.key === 'Enter') {
      // Only an explicitly highlighted suggestion is accepted. With nothing
      // highlighted — the state after simply typing — Enter is left alone so
      // the typed text submits as itself.
      if (open && active >= 0 && active < options.length) {
        e.preventDefault();
        accept(options[active]);
      }
    }
  };

  return (
    <Field className={className}>
      <FieldLabel htmlFor={inputId} optional={!required}>
        {label}
      </FieldLabel>
      <div ref={rootRef} className="relative">
        <Input
          id={inputId}
          type="text"
          role="combobox"
          autoComplete="off"
          aria-expanded={listVisible}
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={active >= 0 ? `${listId}-${active}` : undefined}
          data-testid={testId}
          value={value}
          disabled={disabled}
          required={required}
          maxLength={maxLength}
          placeholder={placeholder}
          onChange={(e) => {
            onChange(e.target.value);
            setOpen(true);
            setActive(-1);
          }}
          onFocus={() => setOpen(true)}
          onKeyDown={onKeyDown}
        />
        {listVisible && (
          <ul
            id={listId}
            role="listbox"
            aria-label="Ticket suggestions"
            data-testid={`${testId}-suggestions`}
            className={cn(
              'absolute z-30 mt-1 max-h-64 w-full min-w-[18rem] overflow-auto rounded-[var(--radius-md)]',
              'border border-[var(--color-border)] bg-[var(--color-surface)] py-1 shadow-[var(--shadow-lg)]',
            )}
          >
            {options.map((s, i) => (
              <li
                key={s.ticket_id}
                id={`${listId}-${i}`}
                role="option"
                aria-selected={i === active}
                data-testid={`${testId}-option-${s.ref}`}
                // Keep focus in the input: a blur here would tear the list
                // down before the click landed.
                onMouseDown={(e) => e.preventDefault()}
                onMouseEnter={() => setActive(i)}
                onClick={() => accept(s)}
                className={cn(
                  'cursor-pointer px-[var(--space-3)] py-[var(--space-2)] text-left',
                  'hover:bg-[var(--color-surface-hover)]',
                  i === active && 'bg-[var(--color-surface-hover)]',
                )}
              >
                <span className="flex items-center gap-[var(--space-2)]">
                  <span className="shrink-0 font-mono text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                    {s.ref}
                  </span>
                  <span className="min-w-0 flex-1 truncate text-[var(--text-sm)] text-[var(--color-text-muted)]">
                    {s.title}
                  </span>
                  {s.assigned_to_me && (
                    <span
                      data-testid={`${testId}-option-${s.ref}-assigned`}
                      className={cn(
                        'shrink-0 rounded-[var(--radius-full)] bg-[var(--color-primary-muted)]',
                        'px-2 py-0.5 text-[10px] font-medium text-[var(--color-primary)]',
                      )}
                    >
                      assigned to you
                    </span>
                  )}
                </span>
                <span className="mt-0.5 block text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  {s.space_key} · {s.status}
                </span>
              </li>
            ))}
            {searching && (
              <li className="px-[var(--space-3)] py-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
                Searching…
              </li>
            )}
            {empty && (
              <li
                data-testid={`${testId}-empty`}
                className="px-[var(--space-3)] py-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-text-muted)]"
              >
                No ticket matches “{q}”. It is recorded exactly as typed.
              </li>
            )}
          </ul>
        )}
      </div>
      {hint && <FieldHint data-testid={`${testId}-hint`}>{hint}</FieldHint>}
    </Field>
  );
}
