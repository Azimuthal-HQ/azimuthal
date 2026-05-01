import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  timeout: 30000,
  globalTimeout: 300000,
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 2 : 4,
  reporter: process.env.CI ? 'github' : 'html',
  use: {
    baseURL: process.env.BASE_URL || 'http://localhost:8082',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    trace: 'on-first-retry',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
  webServer: {
    command: `${process.env.AZIMUTHAL_BINARY || '/tmp/azimuthal-test'} serve`,
    url: 'http://localhost:8082/health',
    reuseExistingServer: !process.env.CI,
    timeout: 60000,
    env: {
      DATABASE_URL: process.env.DATABASE_URL || '',
      JWT_SECRET: process.env.JWT_SECRET || '',
      STORAGE_ENDPOINT: process.env.STORAGE_ENDPOINT || '',
      STORAGE_ACCESS_KEY: process.env.STORAGE_ACCESS_KEY || '',
      STORAGE_SECRET_KEY: process.env.STORAGE_SECRET_KEY || '',
      STORAGE_BUCKET: process.env.STORAGE_BUCKET || '',
      APP_PORT: '8082',
      APP_ENV: 'test',
    },
  },
})
