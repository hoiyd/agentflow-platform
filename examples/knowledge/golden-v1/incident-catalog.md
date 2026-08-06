# Incident Catalog

## PX-2049

`PX-2049` means a payment request reused an idempotency key with a different payload. The operator should compare the original request hash before deciding whether to retry the payment.

## PX-3102

`PX-3102` means the settlement provider did not acknowledge a submitted batch within the expected interval. Check provider status before resubmitting.
