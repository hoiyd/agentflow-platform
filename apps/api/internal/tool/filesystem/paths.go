package filesystem

import (
	"io/fs"
	"strings"

	"agentflow-platform/apps/api/internal/tool"
)

func validatePath(path string) error {
	if path == "" || !fs.ValidPath(path) || len(path) > 1024 || strings.ContainsAny(path, "\\\x00\n\r") || strings.Count(path, "/") > 16 {
		return failure(tool.ErrorSecurityPolicyDenied, "Filesystem path must be a bounded relative path without traversal")
	}
	parts := strings.Split(path, "/")
	for index, part := range parts {
		name := strings.ToLower(part)
		if strings.HasPrefix(name, ".agentflow-") {
			return failure(tool.ErrorSecurityPolicyDenied, "Internal filesystem staging files are not accessible")
		}
		if name == "docs" && index+1 < len(parts) && strings.EqualFold(parts[index+1], "private") {
			return failure(tool.ErrorSecurityPolicyDenied, "Private project notes are not readable")
		}
		if name == ".env" || strings.HasPrefix(name, ".env.") || strings.HasSuffix(name, ".pem") || strings.HasSuffix(name, ".key") || strings.HasSuffix(name, ".p12") || strings.HasSuffix(name, ".pfx") {
			return failure(tool.ErrorSecurityPolicyDenied, "Sensitive filesystem paths are not readable")
		}
		switch name {
		case ".git", ".ssh", ".aws", ".azure", ".config", ".codex", ".gnupg", ".kube", ".data", ".npmrc", ".netrc", ".pypirc", ".git-credentials", ".bash_history", ".zsh_history", "service-account.json", "credentials", "credentials.json", "secrets", "secrets.json", "token", "tokens.json", "id_rsa", "id_ed25519":
			return failure(tool.ErrorSecurityPolicyDenied, "Sensitive filesystem paths are not readable")
		}
	}
	return nil
}
