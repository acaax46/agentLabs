import { cloneElement, useId, useState } from 'react';
import type { ReactElement, ReactNode, Ref } from 'react';
import { APIError } from '../api/client';
import type { Budget } from './api';
import type { Currency } from './model';
import { formatMoney, formatUnitPrice } from './model';
import { CopyButton, Icon, StatusBadge } from './ui';

type FieldInput = {
  id?: string;
  name?: string;
  required?: boolean;
  'aria-describedby'?: string;
  'aria-invalid'?: boolean;
  'aria-label'?: string;
};
export function Field({
  label,
  name,
  required,
  help,
  error,
  errorKey,
  children,
}: {
  label: string;
  name: string;
  required?: boolean;
  help?: ReactNode;
  error?: Error | null;
  errorKey?: string;
  children: ReactElement<FieldInput>;
}) {
  const id = useId();
  const message = error instanceof APIError ? error.fields[errorKey ?? name] : undefined;
  return (
    <label className="wb-field" htmlFor={id}>
      <span>
        {label}
        {(required || children.props.required) && (
          <small className="wb-required" aria-hidden="true">
            必填
          </small>
        )}
      </span>
      {cloneElement(children, {
        id,
        name,
        'aria-label': label,
        required: required || children.props.required,
        'aria-invalid': !!message,
        'aria-describedby':
          [help ? `${id}-help` : '', message ? `${id}-error` : ''].filter(Boolean).join(' ') ||
          undefined,
      })}
      {help && <small id={`${id}-help`}>{help}</small>}
      {message && (
        <small id={`${id}-error`} className="wb-field-error" role="alert">
          {message}
        </small>
      )}
    </label>
  );
}
export function SecretInput({
  name,
  required,
  minLength = 4,
  describedBy,
  id,
  'aria-describedby': description,
  'aria-invalid': invalid,
  'aria-label': label,
  inputRef,
}: {
  name: string;
  required?: boolean;
  minLength?: number;
  describedBy?: string;
  id?: string;
  'aria-describedby'?: string;
  'aria-invalid'?: boolean;
  'aria-label'?: string;
  inputRef?: Ref<HTMLInputElement>;
}) {
  const [show, setShow] = useState(false);
  return (
    <div className="wb-password">
      <input
        id={id}
        ref={inputRef}
        name={name}
        type={show ? 'text' : 'password'}
        required={required}
        minLength={minLength}
        maxLength={8192}
        autoComplete="off"
        spellCheck={false}
        aria-describedby={description || describedBy}
        aria-invalid={invalid}
        aria-label={label}
      />
      <button
        type="button"
        className="wb-button wb-button--ghost wb-button--sm"
        aria-label={show ? '隐藏密钥' : '显示密钥'}
        aria-pressed={show}
        onClick={() => setShow(!show)}
      >
        <Icon name={show ? 'eyeOff' : 'eye'} size={16} />
      </button>
    </div>
  );
}
export function Money({ value, currency }: { value: string | null; currency: Currency }) {
  return value === null ? (
    <span>未知 {currency}</span>
  ) : (
    <details className="wb-money">
      <summary title="展开精确金额">
        {formatMoney(value, currency)} {currency}
      </summary>
      <div>
        <span className="wb-mono">
          {formatUnitPrice(value, currency)} {currency}
        </span>
        <small>{value} 微单位</small>
        <CopyButton
          value={`${formatUnitPrice(value, currency)} ${currency} (${value} micros)`}
          label="复制精确金额"
        />
      </div>
    </details>
  );
}
export function BudgetBar({ budget }: { budget: Budget }) {
  if (!budget.configured) return <p className="wb-muted">项目预算未配置，无法提交运行。</p>;
  const limit = BigInt(budget.limitMicros);
  const spent = BigInt(budget.spentMicros);
  const held = BigInt(budget.heldMicros);
  const ratio = (value: bigint) => (limit > 0n ? Number((value * 10000n) / limit) / 100 : 0);
  const blocked = limit <= spent + held;
  return (
    <div className="wb-budget-breakdown">
      <div
        className="wb-budget-segments"
        role="img"
        aria-label={`预算已结算 ${formatUnitPrice(budget.spentMicros, budget.currency)}，预占 ${formatUnitPrice(budget.heldMicros, budget.currency)}，剩余 ${formatUnitPrice(budget.availableMicros, budget.currency)} ${budget.currency}`}
      >
        <span className="wb-budget-settled" style={{ width: `${Math.min(100, ratio(spent))}%` }} />
        <span
          className="wb-budget-held"
          style={{ width: `${Math.min(100 - ratio(spent), ratio(held))}%` }}
        />
      </div>
      <dl className="wb-fact-list">
        <div>
          <dt>已结算</dt>
          <dd>
            <Money value={budget.spentMicros} currency={budget.currency} />
          </dd>
        </div>
        <div>
          <dt>预占</dt>
          <dd>
            <Money value={budget.heldMicros} currency={budget.currency} />
          </dd>
        </div>
        <div>
          <dt>剩余</dt>
          <dd>
            <Money value={budget.availableMicros} currency={budget.currency} />
          </dd>
        </div>
      </dl>
      {(blocked || (limit > 0n && (spent + held) * 100n >= limit * 80n)) && (
        <p className="wb-field-error" role="status">
          <StatusBadge tone="warning">
            {blocked
              ? '额度已用尽，当前不能预占新运行。'
              : '预算已使用或预占达到 80%，请核对剩余额度。'}
          </StatusBadge>
        </p>
      )}
    </div>
  );
}
export function Pagination({
  nextCursor,
  page,
  pending,
  onNext,
  onPrevious,
}: {
  nextCursor?: string;
  page: number;
  pending: boolean;
  onNext: () => void;
  onPrevious: () => void;
}) {
  return (
    <nav className="wb-pagination" aria-label="列表分页">
      <button
        className="wb-button wb-button--secondary"
        disabled={pending || page === 1}
        onClick={onPrevious}
      >
        上一页
      </button>
      <span>第 {page} 页</span>
      <button
        className="wb-button wb-button--secondary"
        disabled={pending || !nextCursor}
        onClick={onNext}
      >
        下一页
      </button>
    </nav>
  );
}
