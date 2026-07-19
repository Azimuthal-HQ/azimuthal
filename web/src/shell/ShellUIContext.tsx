import { createContext, useContext, useMemo, useState } from 'react';

interface ShellUIState {
  /** Mobile-only: whether the sidebar drawer is open (< md viewports). */
  mobileNavOpen: boolean;
  setMobileNavOpen: (open: boolean) => void;
}

const ShellUIContext = createContext<ShellUIState | null>(null);

/** ShellUIProvider shares top-bar ↔ sidebar UI state (mobile drawer toggle). */
export function ShellUIProvider({ children }: { children: React.ReactNode }) {
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const value = useMemo(() => ({ mobileNavOpen, setMobileNavOpen }), [mobileNavOpen]);
  return <ShellUIContext.Provider value={value}>{children}</ShellUIContext.Provider>;
}

/** useShellUI reads the shell UI state; safe no-op defaults outside the provider (tests). */
export function useShellUI(): ShellUIState {
  const ctx = useContext(ShellUIContext);
  return ctx ?? { mobileNavOpen: false, setMobileNavOpen: () => {} };
}
