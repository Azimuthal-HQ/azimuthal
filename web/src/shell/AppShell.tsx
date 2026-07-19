import { Outlet } from 'react-router-dom';
import { TopBar } from './TopBar';
import { ShellUIProvider } from './ShellUIContext';

/**
 * AppShell is the authenticated application frame: the persistent top bar
 * above everything, with layouts (HomeLayout, SpaceLayout) rendering into
 * the outlet below it.
 */
export function AppShell() {
  return (
    <ShellUIProvider>
      <TopBar />
      <Outlet />
    </ShellUIProvider>
  );
}
