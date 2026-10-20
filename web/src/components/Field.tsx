import { useId, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes } from 'react';

type FieldProps = InputHTMLAttributes<HTMLInputElement> & {
  label: string;
  hint?: ReactNode;
  error?: string;
};

/**
 * A labelled input. The hint and the error are tied to the input with
 * aria-describedby, so screen readers read them with the field.
 */
export function Field({ label, hint, error, id, className, ...input }: FieldProps) {
  const autoId = useId();
  const inputId = id ?? autoId;
  const hintId = hint ? `${inputId}-hint` : undefined;
  const errorId = error ? `${inputId}-error` : undefined;
  const describedBy = [hintId, errorId].filter(Boolean).join(' ') || undefined;

  return (
    <div className={`field${error ? ' field-invalid' : ''}${className ? ` ${className}` : ''}`}>
      <label htmlFor={inputId}>{label}</label>
      <input id={inputId} aria-invalid={error ? true : undefined} aria-describedby={describedBy} {...input} />
      {hint && (
        <p id={hintId} className="field-hint">
          {hint}
        </p>
      )}
      {error && (
        <p id={errorId} className="field-error">
          {error}
        </p>
      )}
    </div>
  );
}

type SelectFieldProps = SelectHTMLAttributes<HTMLSelectElement> & {
  label: string;
  hint?: ReactNode;
  error?: string;
  options: { value: string | number; label: string }[];
  /** Label of an extra first option with an empty value, such as "Any status". */
  placeholder?: string;
};

/** The select twin of Field, with the same labelling and error wiring. */
export function SelectField({ label, hint, error, id, options, placeholder, className, ...select }: SelectFieldProps) {
  const autoId = useId();
  const selectId = id ?? autoId;
  const hintId = hint ? `${selectId}-hint` : undefined;
  const errorId = error ? `${selectId}-error` : undefined;
  const describedBy = [hintId, errorId].filter(Boolean).join(' ') || undefined;
  return (
    <div className={`field${error ? ' field-invalid' : ''}${className ? ` ${className}` : ''}`}>
      <label htmlFor={selectId}>{label}</label>
      <select id={selectId} aria-invalid={error ? true : undefined} aria-describedby={describedBy} {...select}>
        {placeholder !== undefined && <option value="">{placeholder}</option>}
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      {hint && (
        <p id={hintId} className="field-hint">
          {hint}
        </p>
      )}
      {error && (
        <p id={errorId} className="field-error">
          {error}
        </p>
      )}
    </div>
  );
}
