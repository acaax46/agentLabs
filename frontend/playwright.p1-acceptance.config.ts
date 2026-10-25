import { defineConfig } from '@playwright/test';
import base from './playwright.acceptance.config';

export default defineConfig({
  ...base,
  testMatch: '**/p1-tools.spec.ts',
  outputDir: '../.cache/p1-acceptance-results',
});
