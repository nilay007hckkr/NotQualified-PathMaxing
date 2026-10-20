import { useId } from 'react';
import { PERIODS, formatTime, type PeriodKey } from '../lib/format';

/** A segmented control built from radio buttons, so arrow keys and screen readers work for free. */
export function Segmented<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: { value: T; label: string }[];
  onChange: (value: T) => void;
}) {
  const name = useId();
  return (
    <fieldset className="segmented">
      <legend className="sr-only">{label}</legend>
      {options.map((o) => (
        <label key={o.value} className={o.value === value ? 'is-selected' : undefined}>
          <input type="radio" name={name} value={o.value} checked={o.value === value} onChange={() => onChange(o.value)} />
          {o.label}
        </label>
      ))}
    </fieldset>
  );
}

export function PeriodPicker({ value, onChange }: { value: PeriodKey; onChange: (key: PeriodKey) => void }) {
  return <Segmented label="Period" value={value} options={PERIODS.map((p) => ({ value: p.key, label: p.label }))} onChange={onChange} />;
}

/** Shows that the data is polled, and when it last arrived. */
export function LiveBadge({ updatedAt, fetching }: { updatedAt: number; fetching: boolean }) {
  if (!updatedAt) return null;
  return (
    <span className="live" aria-live="off">
      <span className={`live-dot${fetching ? ' is-fetching' : ''}`} aria-hidden="true" />
      Live · updated {formatTime(updatedAt)} IST
    </span>
  );
}
