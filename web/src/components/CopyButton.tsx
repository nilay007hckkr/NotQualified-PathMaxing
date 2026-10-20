import { useEffect, useState } from 'react';

/** Copies text to the clipboard and says so in a live region, since the button label alone changes silently. */
export function CopyButton({ text, label = 'Copy' }: { text: string; label?: string }) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle');

  useEffect(() => {
    if (state === 'idle') return;
    const t = setTimeout(() => setState('idle'), 2000);
    return () => clearTimeout(t);
  }, [state]);

  const copy = async () => {
    try {
      // The Clipboard API needs a secure context; on plain http it is missing.
      await navigator.clipboard.writeText(text);
      setState('copied');
    } catch {
      setState('failed');
    }
  };

  return (
    <>
      <button type="button" className="btn btn-secondary btn-small" onClick={copy} aria-label={`${label} ${text}`}>
        {state === 'copied' ? 'Copied' : label}
      </button>
      <span role="status" className="sr-only">
        {state === 'copied' ? 'Copied to the clipboard.' : state === 'failed' ? 'Could not copy. Select the link and copy it by hand.' : ''}
      </span>
    </>
  );
}
