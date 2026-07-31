# Authentication Incident Runbook

## AUTH-7F31: Signing-Key Rotation Mismatch

Use this procedure when login requests fail shortly after an authentication
signing-key rotation.

### Symptoms

- The gateway returns `401 invalid_signature` for newly issued sessions.
- Existing sessions continue to work until their tokens expire.
- Failures begin within five minutes of a key rotation.

### Likely Cause

The identity service is signing tokens with the new key while one or more
gateway instances still cache the previous public key set.

### Recovery

1. Confirm the active key ID in the identity service.
2. Compare it with the key IDs loaded by every gateway instance.
3. Refresh the gateway key cache and verify propagation.
4. Reissue one test token and confirm that all gateways accept it.
5. Record the affected instances and cache refresh time in the incident log.

### Escalation

Escalate to the identity platform owner when key propagation remains incomplete
ten minutes after the cache refresh. Do not disable signature verification.
