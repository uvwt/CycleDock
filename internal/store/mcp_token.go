package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/uvwt/CycleDock/internal/store/db"
)

// ConfigureMCPEncryption 使用服务端连接密钥配置 MCP Token 加密。
// 密钥缺失时仍支持匿名 MCP；保存或使用带 Token 的连接会安全失败。
func (s *Store) ConfigureMCPEncryption(encodedKey string) error {
	encodedKey = strings.TrimSpace(encodedKey)
	if encodedKey == "" {
		s.mcpCipher = nil
		return nil
	}
	decoders := []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
		hex.DecodeString,
	}
	var key []byte
	for _, decode := range decoders {
		decoded, err := decode(encodedKey)
		if err == nil && len(decoded) == 32 {
			key = decoded
			break
		}
	}
	if len(key) != 32 {
		return errors.New("CYCLEDOCK_CONNECTION_ENCRYPTION_KEY must encode exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("create MCP token cipher: %w", err)
	}
	s.mcpCipher, err = cipher.NewGCM(block)
	return err
}

func (s *Store) encryptMCPBearerToken(workspaceID, connectionID string, token string) ([]byte, error) {
	if s.mcpCipher == nil {
		return nil, errors.New("MCP token encryption key is not configured")
	}
	nonce := make([]byte, s.mcpCipher.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate MCP nonce: %w", err)
	}
	return s.mcpCipher.Seal(nonce, nonce, []byte(token), []byte("cycledock/mcp/"+workspaceID+"/"+connectionID)), nil
}

// MCPBearerToken 仅供服务端 MCP 测试和运行时使用；不允许序列化到 HTTP 响应。
func (s *Store) MCPBearerToken(connection db.WorkspaceMcpConnection) (string, error) {
	if len(connection.BearerTokenCiphertext) == 0 {
		return "", nil
	}
	if s.mcpCipher == nil {
		return "", errors.New("MCP token encryption key is not configured")
	}
	nonceSize := s.mcpCipher.NonceSize()
	if len(connection.BearerTokenCiphertext) < nonceSize+s.mcpCipher.Overhead() {
		return "", errors.New("invalid MCP token ciphertext")
	}
	ciphertext := connection.BearerTokenCiphertext
	token, err := s.mcpCipher.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], []byte("cycledock/mcp/"+connection.WorkspaceID.String()+"/"+connection.ID.String()))
	if err != nil {
		return "", errors.New("MCP bearer token decryption failed")
	}
	return string(token), nil
}
