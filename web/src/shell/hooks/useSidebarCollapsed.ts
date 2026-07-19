import { useLocalStorageState } from './useLocalStorageState';

const STORAGE_KEY = 'azimuthal-sidebar-collapsed';

/** useSidebarCollapsed persists the icon-rail collapse state of the sidebar. */
export function useSidebarCollapsed(): [boolean, (next: boolean | ((prev: boolean) => boolean)) => void] {
  return useLocalStorageState<boolean>(STORAGE_KEY, false);
}
