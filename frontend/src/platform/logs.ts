export function stripTerminalControls(value: string) {
  // OSC links/titles, CSI commands, remaining escapes, and non-printing control bytes.
  /* eslint-disable no-control-regex -- These patterns intentionally remove untrusted control bytes. */
  return value
    .replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g, '')
    .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '')
    .replace(/\x1b[@-_]/g, '')
    .replace(/[\x00-\x08\x0b-\x1f\x7f-\x9f]/g, '');
}
