import { useEffect, useRef } from 'react';
import './monacoWorkers';
import * as monaco from 'monaco-editor';

const languageByExtension: Record<string, string> = {
  bash: 'shell',
  c: 'c',
  cc: 'cpp',
  cpp: 'cpp',
  cs: 'csharp',
  css: 'css',
  go: 'go',
  h: 'c',
  hpp: 'cpp',
  html: 'html',
  java: 'java',
  js: 'javascript',
  json: 'json',
  jsx: 'javascript',
  md: 'markdown',
  php: 'php',
  py: 'python',
  rb: 'ruby',
  rs: 'rust',
  scss: 'scss',
  sh: 'shell',
  sql: 'sql',
  toml: 'ini',
  ts: 'typescript',
  tsx: 'typescript',
  txt: 'plaintext',
  xml: 'xml',
  yaml: 'yaml',
  yml: 'yaml',
};

export type CodeEditorProps = {
  value: string;
  onChange?: (value: string) => void;
  path?: string;
  language?: string;
  readOnly?: boolean;
  ariaLabel: string;
  height?: number;
};

function languageFor(path: string, language?: string) {
  if (language) return language;
  if (!path) return 'plaintext';
  const name = path.split('/').pop() || path;
  if (name.toLowerCase() === 'dockerfile') return 'dockerfile';
  if (name.toLowerCase() === 'makefile') return 'plaintext';
  const extension = name.includes('.') ? name.split('.').pop()?.toLowerCase() : '';
  return (extension && languageByExtension[extension]) || 'plaintext';
}

function readColor(name: string, fallback: string) {
  const value = window.getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return /^#[0-9a-f]{6}$/i.test(value) ? value.slice(1) : fallback;
}

let themeReady = false;

function themeColors(colors: Record<string, string>): Record<string, string> {
  return Object.fromEntries(
    Object.entries(colors).map(([key, value]) => [
      key,
      value.startsWith('#') ? value : `#${value}`,
    ]),
  );
}

function ensureTheme() {
  if (themeReady) return;
  monaco.editor.defineTheme('agentlab-workspace', {
    base: 'vs-dark',
    inherit: true,
    rules: [
      { token: 'comment', foreground: '8FB3A8', fontStyle: 'italic' },
      { token: 'keyword', foreground: 'F2C05C' },
      { token: 'string', foreground: '9FE3C9' },
      { token: 'number', foreground: 'F0A58E' },
      { token: 'type', foreground: 'F5D98B' },
      { token: 'function', foreground: '8FC9EE' },
      { token: 'variable', foreground: 'E3F2EC' },
      { token: 'constant', foreground: 'F0A58E' },
      { token: 'operator', foreground: 'C9DED6' },
      { token: 'delimiter', foreground: 'A9C3B9' },
    ],
    colors: themeColors({
      'editor.background': readColor('--al-color-workspace-bg', '1E3336'),
      'editor.foreground': readColor('--al-color-workspace-text', 'E3F2EC'),
      'editorLineNumber.foreground': '71938A',
      'editorLineNumber.activeForeground': 'E3F2EC',
      'editorCursor.foreground': 'F2C05C',
      'editor.selectionBackground': '2E5A52',
      'editor.inactiveSelectionBackground': '274A44',
      'editor.lineHighlightBackground': '233D3F',
      'editorIndentGuide.background1': '2C4A46',
      'editorIndentGuide.activeBackground1': '48C2A7',
      'editorGutter.background': '1B2E31',
      'editorWidget.background': '223B3D',
      'editorWidget.border': '33504B',
      'editorSuggestWidget.background': '223B3D',
      'editorSuggestWidget.border': '33504B',
      'editorSuggestWidget.selectedBackground': '2E5A52',
      'editorHoverWidget.background': '223B3D',
      'editorHoverWidget.border': '33504B',
      'input.background': '1B2E31',
      'input.border': '33504B',
      focusBorder: '48C2A7',
      'scrollbarSlider.background': '33504B66',
      'scrollbarSlider.hoverBackground': '33504B99',
      'scrollbarSlider.activeBackground': '33504BBB',
    }),
  });
  themeReady = true;
}

export function CodeEditor({
  value,
  onChange,
  path = '',
  language,
  readOnly = false,
  ariaLabel,
  height = 480,
}: CodeEditorProps) {
  const host = useRef<HTMLDivElement>(null);
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const onChangeRef = useRef(onChange);
  const valueRef = useRef(value);
  const readOnlyRef = useRef(readOnly);
  const ariaLabelRef = useRef(ariaLabel);
  const applyingValue = useRef(false);
  onChangeRef.current = onChange;
  valueRef.current = value;
  readOnlyRef.current = readOnly;
  ariaLabelRef.current = ariaLabel;

  useEffect(() => {
    const hostElement = host.current;
    if (!hostElement) return;
    ensureTheme();
    const model = monaco.editor.createModel(
      valueRef.current,
      languageFor(path, language),
      monaco.Uri.parse(`inmemory://agentlab/${encodeURIComponent(path || 'buffer')}`),
    );
    model.setEOL(monaco.editor.EndOfLineSequence.LF);
    const editor = monaco.editor.create(hostElement, {
      model,
      theme: 'agentlab-workspace',
      ariaLabel: ariaLabelRef.current,
      readOnly: readOnlyRef.current,
      automaticLayout: true,
      fontFamily:
        window
          .getComputedStyle(document.documentElement)
          .getPropertyValue('--al-font-mono')
          .trim() || 'Consolas, monospace',
      fontSize: 14,
      lineHeight: 24,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      padding: { top: 16, bottom: 16 },
      renderLineHighlight: 'line',
      smoothScrolling: true,
      tabSize: 4,
      insertSpaces: true,
      wordWrap: 'off',
      contextmenu: true,
      folding: true,
      glyphMargin: false,
      lineNumbersMinChars: 3,
      overviewRulerLanes: 0,
      hideCursorInOverviewRuler: true,
      overviewRulerBorder: false,
      fixedOverflowWidgets: true,
      scrollbar: {
        verticalScrollbarSize: 10,
        horizontalScrollbarSize: 10,
        useShadows: false,
      },
      accessibilitySupport: 'auto',
      // Keep input/IME behavior consistent across Chromium and Wails WebView2 versions.
      editContext: false,
    });
    editorRef.current = editor;
    const subscription = editor.onDidChangeModelContent(() => {
      if (applyingValue.current) return;
      onChangeRef.current?.(model.getValue());
    });
    return () => {
      subscription.dispose();
      editor.dispose();
      model.dispose();
      editorRef.current = null;
    };
  }, [path, language]);

  useEffect(() => {
    const model = editorRef.current?.getModel();
    if (!model || model.getValue() === value) return;
    applyingValue.current = true;
    model.setValue(value);
    applyingValue.current = false;
  }, [value]);

  useEffect(() => {
    editorRef.current?.updateOptions({ readOnly, ariaLabel });
  }, [readOnly, ariaLabel]);

  return <div className="code-editor" ref={host} style={{ height }} />;
}

export default CodeEditor;
