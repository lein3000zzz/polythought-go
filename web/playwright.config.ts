import { defineConfig, devices } from '@playwright/test'

// End-to-end tests drive a running stack: `make e2e` starts a throwaway one in Docker
// and points them at it. They share its database, so each test makes its own data.
export default defineConfig({
  testDir: 'e2e',
  outputDir: 'test-results',
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  reporter: 'list',
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:3100',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    { name: 'setup', testMatch: /\.setup\.ts$/ },
    {
      name: 'chromium',
      dependencies: ['setup'],
      use: {
        ...devices['Desktop Chrome'],
        viewport: { width: 1440, height: 900 },
        storageState: 'e2e/.auth/admin.json', // signed in as the admin
      },
    },
  ],
})
