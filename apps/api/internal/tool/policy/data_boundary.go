package policy

// TransmitsData follows trusted capability declarations, not Tool names or
// model arguments. It also covers future remote and external-service Bindings.
func TransmitsData(capability Capability) bool {
	capability = NormalizeCapability(capability)
	if capability.Source == SourceRemote || capability.Scope.Network.Mode != NetworkNone {
		return true
	}
	for _, resource := range capability.Scope.Resources {
		if resource.Kind == ResourceExternal {
			return true
		}
	}
	return false
}

// UsesPrivateResources conservatively treats Run, Conversation, Workspace and
// local filesystem results as non-public, including metadata and write receipts.
func UsesPrivateResources(capability Capability) bool {
	for _, resource := range capability.Scope.Resources {
		switch resource.Kind {
		case ResourceRun, ResourceConversation, ResourceWorkspace, ResourceFilesystem:
			return true
		}
	}
	return false
}
