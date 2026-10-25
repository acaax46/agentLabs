import { createContext, useContext, useEffect, useCallback, useState } from 'react';
import type { ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { APIError, getAuthState, login, logout, register } from '../api/client';
import type { AuthState, AuthUser, RegisterInput, Role } from '../api/client';

export const roleLabels: Record<Role, string> = {
  user: '成员',
  admin: '管理员',
};
export const roleHomes: Record<Role, string> = {
  user: '/chat',
  admin: '/overview',
};
export const roleAreas: Record<Role, string> = {
  user: '托管工作台',
  admin: '托管工作台',
};
const blank: AuthState = {
  user: null,
  permissions: {
    catalogRead: [],
    catalogWrite: false,
    recordsRead: [],
    manageUsers: false,
    environment: false,
  },
};
type Context = {
  state: AuthState;
  user: AuthUser | null;
  pending: boolean;
  error: Error | null;
  refresh: () => void;
  signIn: (username: string, password: string) => Promise<void>;
  signUp: (input: RegisterInput) => Promise<void>;
  signOut: () => Promise<void>;
  clear: () => void;
  expired: boolean;
};
const Context = createContext<Context | null>(null);
export function AuthProvider({ children }: { children: ReactNode }) {
  const cache = useQueryClient();
  const [expired, setExpired] = useState(false);
  const query = useQuery({ queryKey: ['auth'], queryFn: getAuthState, retry: 1, staleTime: 30000 });
  const apply = useCallback(
    (state: AuthState) => {
      void cache.cancelQueries({ predicate: (q) => q.queryKey[0] !== 'auth' });
      cache.removeQueries({ predicate: (q) => q.queryKey[0] !== 'auth' });
      cache.setQueryData(['auth'], state);
    },
    [cache],
  );
  useEffect(() => {
    const clear = () => {
      setExpired(true);
      apply(blank);
    };
    window.addEventListener('agentlab:unauthenticated', clear);
    return () => window.removeEventListener('agentlab:unauthenticated', clear);
  }, [apply]);
  const value: Context = {
    state: query.data || blank,
    user: query.data?.user || null,
    pending: query.isPending,
    error: query.error,
    refresh: () => void query.refetch(),
    signIn: async (username, password) => {
      apply(await login(username, password));
      setExpired(false);
    },
    signUp: async (input) => {
      apply(await register(input));
      setExpired(false);
    },
    signOut: async () => {
      try {
        await logout();
      } catch (error) {
        if (!(error instanceof APIError && error.code === 'UNAUTHENTICATED')) throw error;
      }
      apply(blank);
    },
    clear: () => apply(blank),
    expired,
  };
  return <Context.Provider value={value}>{children}</Context.Provider>;
}
export function useAuth() {
  const value = useContext(Context);
  if (!value) throw new Error('Missing AuthProvider');
  return value;
}
