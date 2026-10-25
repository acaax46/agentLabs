export type SheetPreview = {
  name: string;
  columns: string[];
  rows: string[][];
  truncated: boolean;
};
const xmlLimit = 2 * 1024 * 1024;
type Entry = { name: string; method: number; compressed: number; expanded: number; offset: number };
async function bounded(stream: ReadableStream<Uint8Array>) {
  const reader = stream.getReader();
  const parts: Uint8Array[] = [];
  let size = 0;
  try {
    while (true) {
      const next = await reader.read();
      if (next.done) break;
      size += next.value.length;
      if (size > xmlLimit) throw new Error('工作表超出安全预览范围，请下载查看。');
      parts.push(next.value);
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const part of parts) {
    bytes.set(part, offset);
    offset += part.length;
  }
  return new TextDecoder().decode(bytes);
}
function documentXML(text: string) {
  if (/<!DOCTYPE|<!ENTITY/i.test(text)) throw new Error('预览不允许 XML 外部实体。');
  const document = new DOMParser().parseFromString(text, 'application/xml');
  if (document.getElementsByTagName('parsererror').length)
    throw new Error('Excel 文件的 XML 内容无效。');
  return document;
}
const elements = (parent: Document | Element, name: string) =>
  Array.from(parent.getElementsByTagNameNS('*', name));
export async function readSpreadsheet(blob: Blob): Promise<SheetPreview> {
  if (blob.size > 8 * 1024 * 1024) throw new Error('Excel 预览上限为 8 MB，请下载完整文件。');
  const bytes = new Uint8Array(await blob.arrayBuffer());
  const view = new DataView(bytes.buffer);
  let end = -1;
  for (let index = bytes.length - 22; index >= Math.max(0, bytes.length - 65557); index--) {
    if (view.getUint32(index, true) === 0x06054b50) {
      end = index;
      break;
    }
  }
  if (end < 0) throw new Error('不支持此 Excel 格式，请使用未加密的 .xlsx 文件。');
  const count = view.getUint16(end + 10, true);
  let position = view.getUint32(end + 16, true);
  const entries = new Map<string, Entry>();
  if (count > 400 || position >= end) throw new Error('工作簿结构超出安全预览范围。');
  for (let index = 0; index < count; index++) {
    if (position + 46 > end || view.getUint32(position, true) !== 0x02014b50)
      throw new Error('Excel 压缩目录无效。');
    if (view.getUint16(position + 8, true) & 1) throw new Error('加密 Excel 只支持下载。');
    const length = view.getUint16(position + 28, true);
    const extra = view.getUint16(position + 30, true);
    const comment = view.getUint16(position + 32, true);
    if (position + 46 + length + extra + comment > end) throw new Error('Excel 文件目录被截断。');
    const name = new TextDecoder().decode(bytes.slice(position + 46, position + 46 + length));
    entries.set(name, {
      name,
      method: view.getUint16(position + 10, true),
      compressed: view.getUint32(position + 20, true),
      expanded: view.getUint32(position + 24, true),
      offset: view.getUint32(position + 42, true),
    });
    position += 46 + length + extra + comment;
  }
  const read = async (name: string, optional = false) => {
    const entry = entries.get(name);
    if (!entry) {
      if (optional) return null;
      throw new Error('缺少 Excel 工作表内容。');
    }
    if (
      entry.expanded > xmlLimit ||
      entry.offset + 30 >= bytes.length ||
      view.getUint32(entry.offset, true) !== 0x04034b50
    )
      throw new Error('工作表超出安全预览范围。');
    const begin =
      entry.offset +
      30 +
      view.getUint16(entry.offset + 26, true) +
      view.getUint16(entry.offset + 28, true);
    if (begin + entry.compressed > bytes.length) throw new Error('Excel 文件内容被截断。');
    const data = bytes.slice(begin, begin + entry.compressed);
    if (entry.method === 0) {
      if (data.length > xmlLimit) throw new Error('工作表过大。');
      return documentXML(new TextDecoder().decode(data));
    }
    if (entry.method !== 8 || typeof DecompressionStream === 'undefined')
      throw new Error('浏览器不支持此 Excel 压缩格式，请下载查看。');
    return documentXML(
      await bounded(new Blob([data]).stream().pipeThrough(new DecompressionStream('deflate-raw'))),
    );
  };
  const workbook = await read('xl/workbook.xml', true);
  const sheet = workbook ? elements(workbook, 'sheet')[0] : undefined;
  const relations = await read('xl/_rels/workbook.xml.rels', true);
  const relation = relations
    ? elements(relations, 'Relationship').find(
        (item) => item.getAttribute('Id') === sheet?.getAttribute('r:id'),
      )
    : undefined;
  const target = relation?.getAttribute('Target');
  if (relation?.getAttribute('TargetMode') === 'External' || target?.split('/').includes('..'))
    throw new Error('不允许外部工作表引用。');
  const name = target
    ? target.startsWith('/xl/')
      ? target.slice(1)
      : `xl/${target.replace(/^xl\//, '')}`
    : [...entries.keys()].find((name) => /^xl\/worksheets\/[^/]+\.xml$/.test(name));
  if (!name) throw new Error('未找到可预览的工作表。');
  const strings = await read('xl/sharedStrings.xml', true);
  const shared = strings
    ? elements(strings, 'si').map((item) =>
        elements(item, 't')
          .map((text) => text.textContent ?? '')
          .join(''),
      )
    : [];
  const document = await read(name);
  if (!document) throw new Error('工作表为空。');
  const source = elements(document, 'row');
  const rows = source.slice(0, 51).map((row) => {
    const cells = Array<string>(20).fill('');
    for (const cell of elements(row, 'c')) {
      const ref = cell.getAttribute('r')?.match(/^[A-Z]+/)?.[0] ?? '';
      let column = 0;
      for (const char of ref) column = column * 26 + char.charCodeAt(0) - 64;
      if (column < 1 || column > 20) continue;
      const value = elements(cell, 'v')[0]?.textContent ?? '';
      cells[column - 1] =
        cell.getAttribute('t') === 's'
          ? (shared[Number(value)] ?? '')
          : cell.getAttribute('t') === 'inlineStr'
            ? elements(cell, 't')
                .map((item) => item.textContent ?? '')
                .join('')
            : value;
    }
    return cells;
  });
  let width = 1;
  rows.forEach((row) =>
    row.forEach((value, index) => {
      if (value) width = Math.max(width, index + 1);
    }),
  );
  return {
    name: sheet?.getAttribute('name') ?? '首个工作表',
    columns: Array.from({ length: width }, (_, index) => String.fromCharCode(65 + index)),
    rows: rows.slice(0, 50).map((row) => row.slice(0, width)),
    truncated:
      source.length > 50 ||
      elements(document, 'c').some(
        (cell) =>
          (cell.getAttribute('r')?.match(/^[A-Z]+/)?.[0]?.length ?? 0) > 1 ||
          /^U|^V|^W|^X|^Y|^Z/.test(cell.getAttribute('r') ?? ''),
      ),
  };
}
