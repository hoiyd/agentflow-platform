package store

import (
	"crypto/rand"
	"encoding/hex"

	"strings"

	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func NormalizeWorkspaceID(workspaceID string) string {
	return domain.NormalizeWorkspaceID(workspaceID)
}

func NormalizeTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "New conversation"
	}
	runes := []rune(title)
	if len(runes) > 48 {
		return string(runes[:48]) + "..."
	}
	return title
}

func NewID(prefix string) string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return prefix + "_" + time.Now().UTC().Format("20060102150405")
	}
	return prefix + "_" + hex.EncodeToString(bytes[:])
}
