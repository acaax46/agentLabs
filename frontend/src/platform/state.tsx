import { createContext, useContext, useState } from 'react';
import type { ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useAuth } from '../auth/context';
import { get } from './api';
import type { Me, Page, Project, Readiness } from './api';

type ContextValue = {
  projects: Project[];
  project: Project | null;
  projectId: string;
  selectProject: (id: string) => void;
  pending: boolean;
  error: Error | null;
  readiness: Readiness | undefined;
  me: Me | undefined;
  refresh: () => void;
};
const Context = createContext<ContextValue | null>(null);
export function PlatformProvider({ children }: { children: ReactNode }) {
  const { user } = useAuth();
  const cache = useQueryClient();
  const [selected, setSelected] = useState('');
  const projects = useQuery({
    queryKey: ['platform', 'projects', user?.id],
    queryFn: ({ signal }) =>
      get<Page<Project>>(
        '/projects',
        { all: user?.role === 'admin' ? 'true' : undefined, limit: 100 },
        signal,
      ),
    enabled: !!user,
    retry: false,
  });
  const readiness = useQuery({
    queryKey: ['platform', 'capabilities', user?.id],
    queryFn: ({ signal }) => get<Readiness>('/capabilities', {}, signal),
    enabled: !!user,
    retry: false,
    refetchInterval: 30_000,
  });
  const me = useQuery({
    queryKey: ['platform', 'me', user?.id],
    queryFn: ({ signal }) => get<Me>('/me', {}, signal),
    enabled: !!user,
    retry: false,
  });
  const items = projects.data?.items ?? [];
  const selectedProject = useQuery({
    queryKey: ['platform', 'project', selected],
    queryFn: ({ signal }) => get<Project>(`/projects/${encodeURIComponent(selected)}`, {}, signal),
    enabled: !!user && !!selected && !items.some((item) => item.id === selected),
    retry: false,
  });
  const project =
    items.find((item) => item.id === selected) ?? selectedProject.data ?? items[0] ?? null;
  return (
    <Context.Provider
      value={{
        projects:
          project && !items.some((item) => item.id === project.id) ? [project, ...items] : items,
        project,
        projectId: project?.id ?? '',
        selectProject: setSelected,
        pending: projects.isPending,
        error: projects.error,
        readiness: readiness.isError ? undefined : readiness.data,
        me: me.isError ? undefined : me.data,
        refresh: () => {
          void cache.invalidateQueries({ queryKey: ['platform'] });
        },
      }}
    >
      {children}
    </Context.Provider>
  );
}
export function usePlatform() {
  const context = useContext(Context);
  if (!context) throw new Error('Missing PlatformProvider');
  return context;
}
