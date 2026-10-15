package policy

// AllowsPlanning trusts validated local capabilities, never concurrency hints,
// model instructions or remote read-only annotations. Existing grants still apply.
func AllowsPlanning(c Capability) bool {
	c = NormalizeCapability(c)
	if c.Source != SourceLocal || c.SideEffect != SideEffectNone {
		return false
	}
	for _, r := range c.Scope.Resources {
		if r.Access != AccessRead {
			return false
		}
	}
	return true
}
