import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { platformPath } from './api';
import type { RunEvent } from './api';

export function useConversationEvents(runId: string, active: boolean) {
  const cache = useQueryClient();
  const [state, setState] = useState<{ id: string; events: RunEvent[]; reconnecting: boolean }>({
    id: '',
    events: [],
    reconnecting: false,
  });
  useEffect(() => {
    if (!runId) return;
    const controller = new AbortController();
    let cursor = 0;
    const events: RunEvent[] = [];
    let retry: ReturnType<typeof setTimeout> | undefined;
    const refresh = () => {
      void cache.invalidateQueries({ queryKey: ['platform', 'conversation'] });
      void cache.invalidateQueries({ queryKey: ['platform', 'chat-files', runId] });
    };
    const consume = (frame: string) => {
      const type = frame
        .split('\n')
        .find((line) => line.startsWith('event:'))
        ?.slice(6)
        .trim();
      if (type === 'end') {
        refresh();
        return true;
      }
      if (type === 'error') throw new Error('stream interrupted');
      const payload = frame
        .split('\n')
        .filter((line) => line.startsWith('data:'))
        .map((line) => line.slice(5).trimStart())
        .join('\n');
      if (!payload) return false;
      const event = JSON.parse(payload) as RunEvent;
      if (event.seq > cursor) {
        cursor = event.seq;
        events.push(event);
        setState({ id: runId, events: [...events], reconnecting: false });
        if (
          [
            'artifact.created',
            'artifact.ready',
            'run.succeeded',
            'run.failed',
            'run.cancelled',
          ].includes(event.type)
        )
          refresh();
      }
      return false;
    };
    const connect = async () => {
      try {
        const response = await fetch(
          platformPath(`/runs/${encodeURIComponent(runId)}/events`, { after: cursor }),
          {
            credentials: 'same-origin',
            cache: 'no-store',
            signal: controller.signal,
            headers: { Accept: 'text/event-stream', 'X-AgentLab-Request': 'platform' },
          },
        );
        if (!response.ok) {
          if (response.status === 401) window.dispatchEvent(new Event('agentlab:unauthenticated'));
          throw new Error('event request failed');
        }
        if (!response.body || !response.headers.get('Content-Type')?.includes('text/event-stream'))
          throw new Error('event stream unavailable');
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        try {
          while (!controller.signal.aborted) {
            const { value, done } = await reader.read();
            buffer += decoder.decode(value, { stream: !done }).replace(/\r\n/g, '\n');
            let index: number;
            while ((index = buffer.indexOf('\n\n')) >= 0) {
              const frame = buffer.slice(0, index);
              buffer = buffer.slice(index + 2);
              if (consume(frame)) return;
            }
            if (buffer.length > 1 << 20) throw new Error('event too large');
            if (done) {
              if (!active) return;
              throw new Error('stream closed');
            }
          }
        } finally {
          await reader.cancel().catch(() => undefined);
        }
      } catch {
        if (!controller.signal.aborted) {
          setState({ id: runId, events: [...events], reconnecting: true });
          retry = setTimeout(() => void connect(), 2000);
        }
      }
    };
    setState({ id: runId, events: [], reconnecting: false });
    void connect();
    return () => {
      controller.abort();
      if (retry) clearTimeout(retry);
    };
  }, [runId, active, cache]);
  return state.id === runId ? state : { id: runId, events: [], reconnecting: false };
}
