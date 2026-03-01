import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';

// Vitest does not clean up rendered components between tests by itself.
afterEach(() => {
  cleanup();
});
