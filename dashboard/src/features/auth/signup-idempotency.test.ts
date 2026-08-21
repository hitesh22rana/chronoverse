import { describe, expect, it } from "vitest"

import { fingerprintSignupCredentials, selectSignupAttempt } from "./signup-idempotency"

describe("signup idempotency", () => {
  it("reuses the command key for the same credentials", async () => {
    const fingerprint = await fingerprintSignupCredentials({ email: "person@example.com", password: "password123" })
    const first = selectSignupAttempt(null, fingerprint, () => "first-key")
    const replay = selectSignupAttempt(first, fingerprint, () => "unexpected-key")

    expect(replay).toBe(first)
    expect(replay.idempotencyKey).toBe("first-key")
  })

  it("rotates the command key when credentials change", async () => {
    const firstFingerprint = await fingerprintSignupCredentials({ email: "person@example.com", password: "password123" })
    const changedFingerprint = await fingerprintSignupCredentials({ email: "person@example.com", password: "changed-password123" })
    const first = selectSignupAttempt(null, firstFingerprint, () => "first-key")
    const changed = selectSignupAttempt(first, changedFingerprint, () => "changed-key")

    expect(changed.idempotencyKey).toBe("changed-key")
    expect(changed.credentialFingerprint).not.toBe(first.credentialFingerprint)
  })
})
