// Browser side of passkeys: the server sends base64url fields, WebAuthn wants ArrayBuffers.
const b64 = (s) => Uint8Array.from(atob(s.replace(/-/g, '+').replace(/_/g, '/')), (c) => c.charCodeAt(0)).buffer;
const enc = (buf) => btoa(String.fromCharCode(...new Uint8Array(buf))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');

export const passkeysSupported = () => !!window.PublicKeyCredential;

// friendly turns browser WebAuthn errors into text people understand.
export function friendly(e) {
  if (e?.name === 'NotAllowedError') return 'The passkey request was cancelled or timed out.';
  if (e?.name === 'InvalidStateError') return 'This passkey is already added.';
  if (e?.name === 'SecurityError') return 'Passkeys need a domain name with a trusted certificate (or localhost).';
  return 'Your browser could not use a passkey here.';
}

export async function createPasskey(options) {
  const o = options.publicKey;
  o.challenge = b64(o.challenge);
  o.user.id = b64(o.user.id);
  (o.excludeCredentials || []).forEach((c) => (c.id = b64(c.id)));
  const c = await navigator.credentials.create({ publicKey: o });
  return {
    id: c.id, rawId: enc(c.rawId), type: c.type,
    response: {
      attestationObject: enc(c.response.attestationObject),
      clientDataJSON: enc(c.response.clientDataJSON),
      transports: c.response.getTransports ? c.response.getTransports() : [],
    },
  };
}

export async function getPasskey(options) {
  const o = options.publicKey;
  o.challenge = b64(o.challenge);
  (o.allowCredentials || []).forEach((c) => (c.id = b64(c.id)));
  const c = await navigator.credentials.get({ publicKey: o });
  return {
    id: c.id, rawId: enc(c.rawId), type: c.type,
    response: {
      authenticatorData: enc(c.response.authenticatorData),
      clientDataJSON: enc(c.response.clientDataJSON),
      signature: enc(c.response.signature),
      userHandle: c.response.userHandle ? enc(c.response.userHandle) : null,
    },
  };
}
