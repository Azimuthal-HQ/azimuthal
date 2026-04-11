import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
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
    command: [
      `DATABASE_URL=${process.env.DATABASE_URL}`,
      `JWT_SECRET=${process.env.JWT_SECRET}`,
      `STORAGE_ENDPOINT=${process.env.STORAGE_ENDPOINT}`,
      `STORAGE_ACCESS_KEY=${process.env.STORAGE_ACCESS_KEY}`,
      `STORAGE_SECRET_KEY=${process.env.STORAGE_SECRET_KEY}`,
      `STORAGE_BUCKET=${process.env.STORAGE_BUCKET}`,
      `APP_PORT=8082`,
      'APP_ENV=test /tmp/azimuthal-test serve'
    ].join(' '),
    url: 'http://localhost:8082/health',
    reuseExistingServer: !process.env.CI,
    timeout: 30000,
  },
})
