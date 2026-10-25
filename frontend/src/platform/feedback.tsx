import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';

const Feedback = createContext<(message: string) => void>(() => {});
export function FeedbackProvider({ children }: { children: ReactNode }) {
  const [notice, setNotice] = useState<{ message: string; id: number } | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const remaining = useRef(4000);
  const started = useRef(0);
  const resume = () => {
    clearTimeout(timer.current);
    started.current = Date.now();
    timer.current = setTimeout(() => setNotice(null), remaining.current);
  };
  const pause = () => {
    clearTimeout(timer.current);
    remaining.current = Math.max(0, remaining.current - (Date.now() - started.current));
  };
  const notify = useCallback((message: string) => {
    remaining.current = 4000;
    setNotice({ message, id: Date.now() });
  }, []);
  useEffect(() => {
    if (!notice) return;
    resume();
    return () => clearTimeout(timer.current);
  }, [notice]);
  return (
    <Feedback.Provider value={notify}>
      {children}
      {notice && (
        <div
          className="wb-toast"
          role="status"
          onMouseEnter={pause}
          onMouseLeave={resume}
          onFocus={pause}
          onBlur={resume}
        >
          <span>{notice.message}</span>
          <button
            className="wb-button wb-button--ghost wb-button--sm"
            onClick={() => setNotice(null)}
          >
            关闭提示
          </button>
        </div>
      )}
    </Feedback.Provider>
  );
}
export function useFeedback() {
  return useContext(Feedback);
}
