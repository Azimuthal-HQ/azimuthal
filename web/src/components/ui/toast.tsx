import * as React from 'react';
import { cn } from '../../lib/utils';

type ToastVariant = 'default' | 'success' | 'error' | 'warning';

interface Toast {
  id: string;
  title: string;
  description?: string;
  variant?: ToastVariant;
}

interface ToastContextValue {
  toasts: Toast[];
  toast: (toast: Omit<Toast, 'id'>) => void;
  dismiss: (id: string) => void;
}

const ToastContext = React.createContext<ToastContextValue | undefined>(undefined);

let toastCount = 0;

function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = React.useState<Toast[]>([]);

  const dismiss = React.useCallback((id: string) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const toast = React.useCallback(
    (newToast: Omit<Toast, 'id'>) => {
      const id = `toast-${++toastCount}`;
      setToasts((prev) => [...prev, { ...newToast, id }]);
      setTimeout(() => dismiss(id), 5000);
    },
    [dismiss],
  );

  const value = React.useMemo(
    () => ({ toasts, toast, dismiss }),
    [toasts, toast, dismiss],
  );

  return (
    <ToastContext.Provider value={value}>
      {children}
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </ToastContext.Provider>
  );
}

function useToast() {
  const context = React.useContext(ToastContext);
  if (!context) {
    throw new Error('useToast must be used within a ToastProvider');
  }
  return context;
}

const variantStyles: Record<ToastVariant, string> = {
  default:
    'border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-text)]',
  success:
    'border-[var(--color-success)] bg-[var(--color-success)]/10 text-[var(--color-text)]',
  error:
    'border-[var(--color-danger)] bg-[var(--color-danger)]/10 text-[var(--color-text)]',
  warning:
    'border-[var(--color-warning)] bg-[var(--color-warning)]/10 text-[var(--color-text)]',
};

function ToastViewport({
  toasts,
  onDismiss,
}: {
  toasts: Toast[];
  onDismiss: (id: string) => void;
}) {
  if (toasts.length === 0) return null;

  return (
    <div className="fixed bottom-4 right-4 z-[100] flex flex-col gap-2">
      {toasts.map((t) => (
        <ToastItem key={t.id} toast={t} onDismiss={onDismiss} />
      ))}
    </div>
  );
}

function ToastItem({
  toast: t,
  onDismiss,
}: {
  toast: Toast;
  onDismiss: (id: string) => void;
}) {
  const [visible, setVisible] = React.useState(false);

  React.useEffect(() => {
    const frame = requestAnimationFrame(() => setVisible(true));
    return () => cancelAnimationFrame(frame);
  }, []);

  return (
    <div
      className={cn(
        'pointer-events-auto w-80 rounded-[var(--radius-lg)] border p-4 shadow-[var(--shadow-lg)] transition-all duration-300',
        visible ? 'translate-x-0 opacity-100' : 'translate-x-full opacity-0',
        variantStyles[t.variant ?? 'default'],
      )}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="flex-1">
          <p className="text-[var(--text-sm)] font-semibold">{t.title}</p>
          {t.description && (
            <p className="mt-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
              {t.description}
            </p>
          )}
        </div>
        <button
          onClick={() => onDismiss(t.id)}
          className="shrink-0 text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <path d="M18 6 6 18" />
            <path d="m6 6 12 12" />
          </svg>
        </button>
      </div>
    </div>
  );
}

export { ToastProvider, useToast };
export type { Toast, ToastVariant };
