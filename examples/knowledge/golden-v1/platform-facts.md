# Platform Facts

## Production location

The production control plane runs in the AWS `eu-central-1` region in Frankfurt. The disaster-recovery control plane is maintained separately in `eu-west-1`.

## Audit retention

Production audit events are retained online for 35 days. Older events are moved to an offline archive that is not available through the operator console.
