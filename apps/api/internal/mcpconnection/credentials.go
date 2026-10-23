package mcpconnection

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"

	"agentflow-platform/apps/api/internal/domain"
)

func newCipher(encoded string) (cipher.AEAD, error) {
	if encoded == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("MCP_CREDENTIAL_ENCRYPTION_KEY must be a base64-encoded 32-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func credentialAAD(c domain.MCPConnection) []byte {
	return []byte("agentflow:mcp:v1:" + c.WorkspaceID + ":" + c.URL)
}

func (s *Service) encrypt(c domain.MCPConnection, secret string) (string, error) {
	if s.cipher == nil {
		return "", &Error{Status: 503, Message: "Operator must configure MCP_CREDENTIAL_ENCRYPTION_KEY before storing Bearer tokens"}
	}
	nonce := make([]byte, s.cipher.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", errors.New("MCP credential encryption failed")
	}
	return base64.StdEncoding.EncodeToString(s.cipher.Seal(nonce, nonce, []byte(secret), credentialAAD(c))), nil
}

func (s *Service) decrypt(c domain.MCPConnection) (string, error) {
	if c.CredentialCiphertext == "" {
		return "", nil
	}
	if s.cipher == nil {
		return "", &Error{Status: 503, Message: "MCP credential key is unavailable"}
	}
	data, err := base64.StdEncoding.DecodeString(c.CredentialCiphertext)
	if err != nil || len(data) < s.cipher.NonceSize() {
		return "", errors.New("MCP credential cannot be decrypted")
	}
	secret, err := s.cipher.Open(nil, data[:s.cipher.NonceSize()], data[s.cipher.NonceSize():], credentialAAD(c))
	if err != nil {
		return "", errors.New("MCP credential cannot be decrypted")
	}
	return string(secret), nil
}
