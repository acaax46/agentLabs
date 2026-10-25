/* eslint-disable no-control-regex -- Normalize control bytes in exported names and spreadsheet cells. */
export function saveBlob(name: string, blob: Blob) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = name;
  anchor.rel = 'noreferrer';
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 30_000);
}
export function saveText(name: string, text: string, type = 'text/plain;charset=utf-8') {
  saveBlob(name, new Blob([text], { type }));
}
export function csv(headers: string[], rows: (string | number | null | undefined)[][]) {
  const cell = (value: string | number | null | undefined) => {
    let text = value == null ? '' : String(value);
    if (/^[\s]*[=+\-@]/.test(text) || /^[\t\r]/.test(text)) text = `'${text}`;
    return `"${text.replaceAll('"', '""')}"`;
  };
  return '\uFEFF' + [headers, ...rows].map((row) => row.map(cell).join(',')).join('\r\n');
}
function crc32(bytes: Uint8Array) {
  let crc = 0xffffffff;
  for (const byte of bytes) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
  }
  return (crc ^ 0xffffffff) >>> 0;
}
// ZIP32, stored entries and UTF-8 filenames. Export sizes are capped by the caller.
export function makeZip(files: { name: string; bytes: Uint8Array }[]) {
  const parts: Uint8Array[] = [];
  const central: Uint8Array[] = [];
  let offset = 0;
  const used = new Set<string>();
  files.forEach((file, index) => {
    let name =
      file.name
        .split(/[\\/]/)
        .pop()
        ?.replace(/[\u0000-\u001f<>:"|?*]/g, '_') || `file-${index + 1}`;
    if (name === '.' || name === '..') name = `file-${index + 1}`;
    const base = name;
    let suffix = 1;
    while (used.has(name)) name = `${suffix++}-${base}`;
    used.add(name);
    const filename = new TextEncoder().encode(name);
    const checksum = crc32(file.bytes);
    const local = new Uint8Array(30 + filename.length);
    const view = new DataView(local.buffer);
    view.setUint32(0, 0x04034b50, true);
    view.setUint16(4, 20, true);
    view.setUint16(6, 0x800, true);
    view.setUint16(12, 33, true);
    view.setUint32(14, checksum, true);
    view.setUint32(18, file.bytes.length, true);
    view.setUint32(22, file.bytes.length, true);
    view.setUint16(26, filename.length, true);
    local.set(filename, 30);
    parts.push(local, file.bytes);
    const entry = new Uint8Array(46 + filename.length);
    const v = new DataView(entry.buffer);
    v.setUint32(0, 0x02014b50, true);
    v.setUint16(4, 20, true);
    v.setUint16(6, 20, true);
    v.setUint16(8, 0x800, true);
    v.setUint32(16, checksum, true);
    v.setUint32(20, file.bytes.length, true);
    v.setUint32(24, file.bytes.length, true);
    v.setUint16(28, filename.length, true);
    v.setUint32(42, offset, true);
    entry.set(filename, 46);
    central.push(entry);
    v.setUint16(14, 33, true);
    offset += local.length + file.bytes.length;
  });
  const end = new Uint8Array(22);
  const last = new DataView(end.buffer);
  last.setUint32(0, 0x06054b50, true);
  last.setUint16(8, files.length, true);
  last.setUint16(10, files.length, true);
  last.setUint32(
    12,
    central.reduce((sum, entry) => sum + entry.length, 0),
    true,
  );
  last.setUint32(16, offset, true);
  return new Blob(
    [...parts, ...central, end].map((part) => new Uint8Array(part).buffer),
    { type: 'application/zip' },
  );
}
