const cspNonceMetaSelector = 'meta[name="etherview-csp-nonce"]';
const cspNoncePattern = /^[A-Za-z0-9_-]{43}$/u;

export function getDocumentCSPNonce(): string | undefined {
  if (typeof document === "undefined") return undefined;
  const nonce = document.querySelector<HTMLMetaElement>(cspNonceMetaSelector)?.content.trim();
  return nonce && cspNoncePattern.test(nonce) ? nonce : undefined;
}
