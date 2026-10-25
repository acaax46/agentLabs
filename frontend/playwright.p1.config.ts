import { defineConfig } from '@playwright/test';
import base from './playwright.platform.config';

export default defineConfig({
  ...base,
  testMatch: '**/p1.spec.ts',
  outputDir: '../.cache/p1-ui-results',
});
