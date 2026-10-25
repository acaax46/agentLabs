import { defineConfig } from '@playwright/test';
import base from './playwright.platform.config';

export default defineConfig({
  ...base,
  testIgnore: '**/p1.spec.ts',
  outputDir: '../.cache/p1-regression-results',
});
