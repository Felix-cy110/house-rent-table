import { defineConfig } from '@playwright/test';
import { fileURLToPath } from 'node:url';

export default defineConfig({
  testDir: './tests',
  timeout: 30_000,
  workers: 2,
  fullyParallel: true,
  reporter: 'list',
  use: {
    baseURL: 'http://127.0.0.1:18080',
    browserName: 'chromium',
    viewport: { width: 1280, height: 900 },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  webServer: {
    command: 'go run ./cmd/server -addr 127.0.0.1:18080 -codex-bin __missing_codex_for_browser_tests__ -codex-home .work/codex-browser-tests',
    cwd: fileURLToPath(new URL('..', import.meta.url)),
    url: 'http://127.0.0.1:18080/api/health',
    reuseExistingServer: false,
    timeout: 120_000,
  },
});
