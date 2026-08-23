export type SignupAttempt = {
  credentialFingerprint: string
  idempotencyKey: string
}

type SignupCredentials = {
  email: string
  password: string
}

export async function fingerprintSignupCredentials(credentials: SignupCredentials) {
  if (typeof globalThis.crypto?.subtle?.digest !== "function") {
    throw new Error("secure signup retry identification is unavailable")
  }

  const encoded = new TextEncoder().encode(JSON.stringify([credentials.email, credentials.password]))
  const digest = await globalThis.crypto.subtle.digest("SHA-256", encoded)
  return Array.from(new Uint8Array(digest), (value) => value.toString(16).padStart(2, "0")).join("")
}

export function selectSignupAttempt(
  previous: SignupAttempt | null,
  credentialFingerprint: string,
  createKey: () => string,
): SignupAttempt {
  if (previous?.credentialFingerprint === credentialFingerprint) {
    return previous
  }

  return { credentialFingerprint, idempotencyKey: createKey() }
}
