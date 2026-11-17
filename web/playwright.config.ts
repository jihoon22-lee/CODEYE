import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  testMatch: '*.spec.ts',
  workers: 1,
  retries: 0,
  timeout: 30_000,
  globalTimeout: 90_000,
  forbidOnly: Boolean(process.env.CI),
  reporter: 'list',
  outputDir: '../.codeye/browser-results',
  use: {
    browserName: 'chromium',
    headless: true,
    baseURL: process.env.CODEYE_TEST_BASE_URL,
    trace: 'off',
    screenshot: 'off',
    video: 'off',
  },
});
