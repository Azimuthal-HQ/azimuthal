import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  timeout: Number(process.env.PW_TEST_TIMEOUT || 30000),
  // Whole-suite budget. The default assumes fast local hardware; slower
  // machines (small CI boxes, sandboxed containers) can raise it via env
  // without touching this file. Raising a budget is not weakening a test.
  globalTimeout: Number(process.env.PW_GLOBAL_TIMEOUT || 300000),
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 2 : 4,
  reporter: process.env.CI ? 'github' : 'html',
  use: {
    baseURL: process.env.BASE_URL || `http://localhost:${process.env.E2E_PORT || '8082'}`,
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    trace: 'on-first-retry',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
  webServer: {
    command: `${process.env.AZIMUTHAL_BINARY || '/tmp/azimuthal-test'} serve`,
    url: `http://localhost:${process.env.E2E_PORT || '8082'}/health`,
    reuseExistingServer: !process.env.CI,
    timeout: 60000,
    env: {
      DATABASE_URL: process.env.DATABASE_URL || '',
      STORAGE_ENDPOINT: process.env.STORAGE_ENDPOINT || '',
      STORAGE_ACCESS_KEY: process.env.STORAGE_ACCESS_KEY || '',
      STORAGE_SECRET_KEY: process.env.STORAGE_SECRET_KEY || '',
      STORAGE_BUCKET: process.env.STORAGE_BUCKET || '',
      APP_PORT: process.env.E2E_PORT || '8082',
      APP_ENV: 'test',
      // APP_BASE_URL is what the server interpolates into every emailed link
      // it builds — the portal sign-in link and the invite link both. It
      // defaults to http://localhost:8080 (internal/config), while this server
      // binds E2E_PORT, so without this a captured magic link points at the
      // wrong port. The dangerous failure is not a connection refused: it is a
      // real dev server answering on 8080, where the test would navigate
      // somewhere else entirely and pass for the wrong reason.
      //
      // Specs should STILL navigate by pathname rather than by the absolute
      // URL (see web/e2e/admin.spec.ts's invite flow) — that resolves against
      // use.baseURL and is port-correct by construction. Both, deliberately.
      APP_BASE_URL: `http://localhost:${process.env.E2E_PORT || '8082'}`,
      // Link delivery discloses the sign-in URL in the response instead of
      // sending mail, which is how a browser test signs a requester in without
      // a mailbox. It is already the default and APP_ENV=test keeps disclosure
      // on; declaring it makes the dependency visible and survives a change of
      // default.
      AZIMUTHAL_PORTAL_LINK_DELIVERY: 'link',
    },
  },
})
