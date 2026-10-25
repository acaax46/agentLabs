import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { allRecords } from '../api/client';
import type { Sample } from '../api/client';
import { LazyMarkdown } from '../components/LazyMarkdown';
import type { Field } from './modules';
import { formatDate, moduleMap } from './modules';

type FieldProps = {
  field: Field;
  value: unknown;
  onChange: (value: unknown) => void;
  error?: string;
};
function OptionsField({ field, value, onChange, error }: FieldProps) {
  const query = useQuery({
    queryKey: ['catalog', 'options', field.source],
    queryFn: () => allRecords(field.source!),
    enabled: !!field.source,
  });
  const [search, setSearch] = useState('');
  const options = field.source
    ? (query.data || []).map((record) => ({
        value: record.id,
        label: `${record.code} · ${record.name}`,
      }))
    : field.options || [];
  if (field.source && query.isError)
    return (
      <div className="field-option-error" role="alert">
        关联资料读取失败。
        <button type="button" className="text-button" onClick={() => void query.refetch()}>
          重新读取
        </button>
      </div>
    );
  if (field.type === 'select')
    return (
      <>
        <select
          id={field.key}
          value={String(value || '')}
          required={field.required}
          disabled={!!field.source && query.isPending}
          aria-invalid={!!error}
          aria-describedby={`${field.key}-help ${field.key}-error`}
          onChange={(event) => onChange(event.target.value)}
        >
          <option value="">{query.isPending && field.source ? '正在读取…' : '请选择'}</option>
          {options.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
        {field.source && !query.isPending && !options.length && (
          <p className="field-help">
            还没有可选资料，
            <Link to={`/${field.source}/new`}>先新建{moduleMap[field.source].singular}</Link>。
          </p>
        )}
      </>
    );
  const selected = Array.isArray(value) ? (value as string[]) : [];
  const visible = options.filter((option) =>
    option.label.toLowerCase().includes(search.toLowerCase()),
  );
  const reorder = (index: number, direction: number) => {
    const next = [...selected];
    const target = index + direction;
    if (target < 0 || target >= next.length) return;
    [next[index], next[target]] = [next[target]!, next[index]!];
    onChange(next);
  };
  return (
    <div className="multi-select" aria-describedby={`${field.key}-error`}>
      {field.source && (
        <input
          type="search"
          aria-label={`筛选${field.label}`}
          placeholder="搜索编号或名称"
          value={search}
          onChange={(event) => setSearch(event.target.value)}
        />
      )}
      <div className="multi-options">
        {query.isPending && field.source ? (
          <p className="field-help">正在读取资料…</p>
        ) : visible.length ? (
          visible.map((option) => (
            <label className="checkbox-row" key={option.value}>
              <input
                type="checkbox"
                checked={selected.includes(option.value)}
                onChange={(event) =>
                  onChange(
                    event.target.checked
                      ? [...selected, option.value]
                      : selected.filter((item) => item !== option.value),
                  )
                }
              />
              <span>{option.label}</span>
            </label>
          ))
        ) : (
          <p className="field-help">
            没有匹配的选项。
            {field.source && (
              <Link to={`/${field.source}/new`}>新建{moduleMap[field.source].singular}</Link>
            )}
          </p>
        )}
      </div>
      {selected.length > 0 && (
        <div className="selected-list">
          <span className="field-help">
            已选 {selected.length} 项{field.source === 'problems' ? ' · 可调整题目顺序' : ''}
          </span>
          {selected.map((id, index) => (
            <div key={id} className="selected-item">
              <span>
                {index + 1}. {options.find((option) => option.value === id)?.label || id}
              </span>
              {field.source === 'problems' && (
                <div className="inline-actions">
                  <button
                    type="button"
                    className="text-button"
                    disabled={index === 0}
                    onClick={() => reorder(index, -1)}
                    aria-label={`上移第 ${index + 1} 道题`}
                  >
                    上移
                  </button>
                  <button
                    type="button"
                    className="text-button"
                    disabled={index === selected.length - 1}
                    onClick={() => reorder(index, 1)}
                    aria-label={`下移第 ${index + 1} 道题`}
                  >
                    下移
                  </button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
function SamplesField({
  value,
  onChange,
}: {
  value: unknown;
  onChange: (value: Sample[]) => void;
}) {
  const samples = Array.isArray(value) ? (value as Sample[]) : [];
  const change = (index: number, key: keyof Sample, value: string) =>
    onChange(samples.map((sample, i) => (i === index ? { ...sample, [key]: value } : sample)));
  return (
    <div className="samples-editor">
      {samples.map((sample, index) => (
        <div className="sample-editor" key={index}>
          <div className="sample-heading">
            <strong>样例 {index + 1}</strong>
            <button
              type="button"
              className="text-button danger-text"
              onClick={() => onChange(samples.filter((_, i) => i !== index))}
            >
              移除样例 {index + 1}
            </button>
          </div>
          <div className="form-grid">
            {(['input', 'output', 'explanation'] as const).map((key) => (
              <div className={`field ${key === 'explanation' ? 'full-width' : ''}`} key={key}>
                <label htmlFor={`sample-${index}-${key}`}>
                  样例 {index + 1}
                  {key === 'input' ? ' 输入' : key === 'output' ? ' 输出' : ' 解释'}
                </label>
                <textarea
                  className={key === 'explanation' ? '' : 'code-input'}
                  id={`sample-${index}-${key}`}
                  value={sample[key] || ''}
                  maxLength={key === 'explanation' ? 2000 : 10000}
                  rows={key === 'explanation' ? 2 : 4}
                  onChange={(event) => change(index, key, event.target.value)}
                />
              </div>
            ))}
          </div>
        </div>
      ))}
      <button
        type="button"
        className="button secondary"
        disabled={samples.length >= 10}
        onClick={() => onChange([...samples, { input: '', output: '', explanation: '' }])}
      >
        添加公开样例
      </button>
      <p className="field-help">样例输入与输出中的空格和换行会原样保存。</p>
    </div>
  );
}
function MarkdownTextarea({ field, value, onChange, error }: FieldProps) {
  const [preview, setPreview] = useState(false);
  return (
    <div className="markdown-field">
      <div className="markdown-mode" role="group" aria-label="Markdown 编辑方式">
        <button type="button" aria-pressed={!preview} onClick={() => setPreview(false)}>
          编辑
        </button>
        <button type="button" aria-pressed={preview} onClick={() => setPreview(true)}>
          预览
        </button>
      </div>
      {preview ? (
        <div
          className="markdown-preview"
          id={field.key}
          role="document"
          tabIndex={0}
          aria-label="Markdown 预览"
        >
          <LazyMarkdown
            value={String(value || '')}
            emptyText="还没有输入内容，切换到“编辑”开始撰写。"
          />
        </div>
      ) : (
        <textarea
          id={field.key}
          rows={field.key === 'statement' ? 12 : 5}
          value={String(value || '')}
          maxLength={field.maxLength || 4000}
          aria-invalid={!!error}
          aria-describedby={`${field.key}-help ${field.key}-error`}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
    </div>
  );
}
export function EditorField(props: FieldProps) {
  const { field, value, onChange, error } = props;
  const wide = field.type === 'textarea' || field.type === 'multi' || field.type === 'samples';
  return (
    <div className={`field ${wide ? 'full-width' : ''}`}>
      <label htmlFor={field.key}>
        {field.label}
        {field.required && (
          <span className="required" aria-hidden="true">
            {' '}
            *
          </span>
        )}
      </label>
      {field.type === 'select' || field.type === 'multi' ? (
        <OptionsField {...props} />
      ) : field.type === 'samples' ? (
        <SamplesField value={value} onChange={onChange} />
      ) : field.type === 'textarea' ? (
        field.markdown ? (
          <MarkdownTextarea {...props} />
        ) : (
          <textarea
            id={field.key}
            rows={field.key === 'statement' ? 10 : 4}
            value={String(value || '')}
            maxLength={field.maxLength || 4000}
            aria-invalid={!!error}
            aria-describedby={`${field.key}-help ${field.key}-error`}
            onChange={(event) => onChange(event.target.value)}
          />
        )
      ) : (
        <input
          id={field.key}
          type={
            field.type === 'datetime'
              ? 'datetime-local'
              : field.type === 'number'
                ? 'number'
                : 'text'
          }
          value={
            field.type === 'tags' && Array.isArray(value) ? value.join(', ') : String(value ?? '')
          }
          required={field.required}
          min={field.min}
          max={field.max}
          maxLength={field.maxLength}
          aria-invalid={!!error}
          aria-describedby={`${field.key}-help ${field.key}-error`}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
      <span id={`${field.key}-help`} className="field-help">
        {field.help}
      </span>
      <span id={`${field.key}-error`} className="field-error">
        {error}
      </span>
    </div>
  );
}
export function ReadonlyField({
  field,
  value,
  referenceHref,
}: {
  field: Field;
  value: unknown;
  referenceHref?: (kind: NonNullable<Field['source']>, id: string) => string | undefined;
}) {
  const query = useQuery({
    queryKey: ['catalog', 'options', field.source],
    queryFn: () => allRecords(field.source!),
    enabled: !!field.source,
  });
  let content;
  if (field.source) {
    const ids = Array.isArray(value) ? value : [value].filter(Boolean);
    content = ids.length ? (
      <div className="reference-links">
        {ids.map((id) => {
          const found = query.data?.find((record) => record.id === id);
          const href = referenceHref
            ? referenceHref(field.source!, String(id))
            : `/${field.source}/${String(id)}`;
          const label = found
            ? `${found.code} · ${found.name}`
            : query.isPending
              ? '正在读取关联…'
              : '关联资料暂不可查看';
          return href && found ? (
            <Link key={String(id)} to={href}>
              {label}
            </Link>
          ) : (
            <span key={String(id)}>{label}</span>
          );
        })}
      </div>
    ) : (
      '未关联'
    );
  } else if (field.type === 'samples') {
    const samples = Array.isArray(value) ? (value as Sample[]) : [];
    content = samples.length
      ? samples.map((sample, index) => (
          <div className="sample-view" key={index}>
            <h3>公开样例 {index + 1}</h3>
            <div className="sample-grid">
              <div>
                <span className="field-help">输入</span>
                <pre>{sample.input || '（空输入）'}</pre>
              </div>
              <div>
                <span className="field-help">输出</span>
                <pre>{sample.output || '（空输出）'}</pre>
              </div>
            </div>
            {sample.explanation && <p className="reading-text">{sample.explanation}</p>}
          </div>
        ))
      : '暂无公开样例';
  } else if (field.options) {
    const values = Array.isArray(value) ? value : [value];
    content =
      values
        .map(
          (item) => field.options?.find((option) => option.value === item)?.label || String(item),
        )
        .join('、') || '未填写';
  } else if (field.markdown) {
    content = <LazyMarkdown value={String(value ?? '')} emptyText="尚未填写。" />;
  } else if (Array.isArray(value)) content = value.join('、') || '未填写';
  else if (field.type === 'datetime') content = value ? formatDate(String(value)) : '未设置';
  else content = String(value ?? '') || '未填写';
  return (
    <section
      className={`detail-field ${field.type === 'textarea' || field.type === 'samples' || field.type === 'multi' ? 'full-width' : ''}`}
    >
      <h2>{field.label}</h2>
      <div className={field.type === 'textarea' && !field.markdown ? 'reading-text' : ''}>
        {content}
      </div>
    </section>
  );
}
