import type { Issue } from '../api/types';

// Plain-language explanations for the validation codes in API.md. The
// server's own message is the fallback, so a new code still reads sensibly.
const MESSAGES: Record<string, string> = {
  INVALID_URL: 'This isn’t a valid web address. Use a full URL such as https://example.com/page.',
  SCHEME_NOT_ALLOWED: 'Only http:// and https:// addresses are allowed.',
  USERINFO_NOT_ALLOWED: 'Remove the “user@” part before the host: https://yourbank.example@evil.example really goes to evil.example.',
  SELF_LOOP: 'This is already a short link on this site. Chains of short links aren’t allowed.',
  NOT_PUBLIC: 'This points at a private, local or reserved address that visitors can’t reach.',
  BLOCKED_DOMAIN: 'This domain (or a parent domain) is on the platform blocklist.',
  THREAT_MATCH: 'A threat feed (Google Safe Browsing or URLhaus) lists this destination as dangerous.',
  ALIAS_TAKEN: 'This alias is already taken. Aliases ignore letter case (Sale and sale are the same), and deleted links keep theirs.',
  ALIAS_RESERVED: 'This alias is reserved by the app (for example api, app, admin, login, bio).',
  ALIAS_INVALID: 'Use 3–50 letters, digits, - or _, starting and ending with a letter or digit.',
  EXPIRY_IN_PAST: 'The expiry time is already in the past.',
  EXPIRY_BEFORE_START: 'The link must expire after it starts.',
  CAP_INVALID: 'The click limit must be a whole number of at least 1.',
  TARGET_DEVICE_INVALID: 'Each device target needs a valid device and a valid destination.',
  UNKNOWN_CAMPAIGN: 'No campaign with this name exists in your workspace.',
  DUP_IN_FILE: 'Another row in this file uses the same alias.',
  HTTP_NOT_HTTPS: 'The destination uses http, not https, so the visit isn’t encrypted.',
  PUNYCODE_DOMAIN: 'The domain is written in punycode (xn--). Make sure it isn’t a look-alike of a real brand.',
  LONG_REDIRECT_CHAIN: 'The destination redirects several times before it lands.',
  DESTINATION_UNREACHABLE: 'We couldn’t reach the destination just now. It may be down.',
  ONE_CLICK_NO_PASSWORD: 'A one-click link without a password is used up by whoever opens it first (bots never count).',
};

export function explainCode(code: string, fallback?: string): string {
  return MESSAGES[code] ?? fallback ?? code;
}

export function explain(issue: Issue): string {
  return explainCode(issue.code, issue.message);
}

/** The fields each wizard step owns, used to send a server error back to the right step. */
export const STEP1_FIELDS = ['destination', 'alias'];

export function fieldOf(issue: Issue): string {
  // Target errors may name an index ("targets[1].destination"); the form shows them together.
  return issue.field.startsWith('targets') ? 'targets' : issue.field;
}
