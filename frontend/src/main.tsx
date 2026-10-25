import React from 'react';
import ReactDOM from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { HashRouter } from 'react-router-dom';
import { App } from './App';
import { AuthProvider } from './auth/context';
import { PlatformProvider } from './platform/state';
import { FeedbackProvider } from './platform/feedback';
import './design-tokens.css';
import './workbench.css';
import './tailwind.css';

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 1, staleTime: 15_000 } },
});
const root = document.getElementById('root');
if (!root) throw new Error('Application root is missing');

ReactDOM.createRoot(root).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <HashRouter>
        <AuthProvider>
          <FeedbackProvider>
            <PlatformProvider>
              <App />
            </PlatformProvider>
          </FeedbackProvider>
        </AuthProvider>
      </HashRouter>
    </QueryClientProvider>
  </React.StrictMode>,
);
