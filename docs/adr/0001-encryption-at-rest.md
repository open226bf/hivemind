# 1. Encryption at rest for sensitive data

- **Status:** Accepted (preview / pre-1.0)
- **Date:** 2026-06-18
- **Deciders:** Hivemind maintainers

## Context

The control plane persists sensitive material in Postgres: secret values,
secret-flagged environment variables, per-cluster mutual-TLS material, the
internal agent CA private key, and the sensitive fields of service snapshots.

We want meaningful protection **at rest** without turning Hivemind into a
full secrets-management platform while it is still pre-1.0. The primary threat
we care about is a **leaked database** — a stolen dump/backup, a compromised
read replica, or recovered disk — where the attacker has the data but **not**
the application's key material.

We explicitly do **not** claim parity with HashiCorp Vault here. Vault solves a
broader problem (secret isolation, dynamic/leased secrets, per-secret audit,
policy, key-never-in-app). What we provide is **encryption of stored columns**,
which is closer to Vault's *storage-backend* encryption than to Vault itself.

## Decision

### Now (default, shipped)

Encrypt sensitive columns with **AES-256-GCM** using a single static key,
provided as the base64-encoded 32-byte `AES_KEY`.

- Implemented behind the `persistence.Cipher` interface (`AESCipher`): a random
  96-bit nonce per value, prepended to the ciphertext, base64-encoded. GCM gives
  confidentiality **and** integrity (tamper-evident).
- If `AES_KEY` is unset, a `NopCipher` stores values in plaintext and logs a
  warning. **Development only** — production must set `AES_KEY`.

This keeps the single-binary, zero-dependency deployment story intact.

### Planned (opt-in, not yet built)

Allow operators who run a **Vault** instance to enable **envelope encryption**:
a fresh per-value **DEK** (data key) encrypts the data; a **KEK** (master key)
that **lives in Vault Transit** wraps the DEK. The KEK never enters the Hivemind
process.

- Gated by a config flag, e.g. `SECRETS_KEK_PROVIDER=none|vault-transit`
  (default `none` = the behaviour above). Opt-in only.
- **Vault Transit is the only planned provider.** Cloud KMS / HSM are out of
  scope; the future `KeyManager` (`Wrap`/`Unwrap`) port stays generic so a cloud
  KMS could be a later drop-in, but we ship only `vault-transit`.
- Built on the existing `Cipher` seam (a future `EnvelopeCipher`); the `none`
  path remains exactly today's `AESCipher`, so simple deployments are unaffected.

When this is implemented, two forward-compatibility rules apply:

1. **Version the stored blob.** Today's format is untagged → treat it as legacy
   `v0`; the envelope format carries a `keyID`/version so both coexist.
2. **Provider switch is lazy.** Moving `none` → `vault-transit` re-encrypts on
   write (read old, write new), not as a big-bang migration.

## Consequences

**Positive**

- Mitigates the most common breach (a stolen DB dump/backup/replica is ciphertext).
- Simple, offline, no extra infrastructure for the default deployment.
- The opt-in path gives Vault users the property that matters most — the **KEK
  never leaves Vault**, with rotation/disable and per-operation audit in Vault —
  without moving secret *storage* out of Hivemind.

**Negative / limitations (default mode)**

- The KEK (`AES_KEY`) lives in the app's environment/memory: a **process
  compromise exposes it**. This is column encryption, not secret isolation.
- **No key rotation today** — rotating `AES_KEY` makes existing ciphertext
  unreadable (there is no re-encryption path yet).
- Not a secrets-management system: no dynamic/leased secrets, no per-secret
  policy, no fine-grained "who read what" audit.

**Operational**

- `AES_KEY` is a **critical asset**: store it in a secret manager **separate
  from database backups**. **Losing it = permanent, irreversible data loss** for
  all encrypted columns.
- Production deployments must set `AES_KEY` (the `NopCipher` fallback is
  dev-only).

## When to revisit

- Demand for key rotation, per-secret audit, or dynamic secrets.
- Approaching 1.0 / compliance requirements.
- If users ask for cloud KMS (would reuse the planned `KeyManager` port).

## References

- `internal/adapters/persistence/cipher.go` — `Cipher`, `AESCipher`, `NopCipher`
- `cmd/server/main.go` — `buildCipher` (reads `AES_KEY`)
- Docs: [Security model](https://open226bf.github.io/hivemind-doc/concepts/security/),
  [Production checklist](https://open226bf.github.io/hivemind-doc/operations/production-checklist/)
