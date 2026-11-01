package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

// ConfigureModelEncryption 使用服务端模型主密钥配置 API Key 加密。
// 缺失时仍支持本地无鉴权模型或通过环境变量读取；保存需加密的 API Key 时会报错。
func (s *Store) ConfigureModelEncryption(encodedKey string) error {
	encodedKey = strings.TrimSpace(encodedKey)
	if encodedKey == "" {
		s.modelCipher = nil
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
		return errors.New("model encryption key must encode exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("create model API key cipher: %w", err)
	}
	s.modelCipher, err = cipher.NewGCM(block)
	return err
}

func (s *Store) encryptModelAPIKey(workspaceID, routeID string, key string) ([]byte, error) {
	if s.modelCipher == nil {
		return nil, errors.New("model API key encryption key is not configured (CYCLEDOCK_MODEL_SECRET_KEY)")
	}
	nonce := make([]byte, s.modelCipher.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate model API key nonce: %w", err)
	}
	return s.modelCipher.Seal(nonce, nonce, []byte(key), []byte("cycledock/model_route/"+workspaceID+"/"+routeID)), nil
}

// ModelRouteAPIKey 解密模型路由的 API Key；仅在服务端执行模型调用或连通性检查时使用，绝不返回前端。
func (s *Store) ModelRouteAPIKey(route db.WorkspaceModelRoute) (string, error) {
	if len(route.ApiKeyCiphertext) == 0 {
		return "", nil
	}
	if s.modelCipher == nil {
		return "", errors.New("model API key encryption key is not configured (CYCLEDOCK_MODEL_SECRET_KEY)")
	}
	nonceSize := s.modelCipher.NonceSize()
	if len(route.ApiKeyCiphertext) < nonceSize+s.modelCipher.Overhead() {
		return "", errors.New("invalid model API key ciphertext")
	}
	ciphertext := route.ApiKeyCiphertext
	key, err := s.modelCipher.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], []byte("cycledock/model_route/"+route.WorkspaceID.String()+"/"+route.ID.String()))
	if err != nil {
		return "", errors.New("model API key decryption failed")
	}
	return string(key), nil
}

// GetModelRoute 根据 Workspace 与 Route ID 获取模型路由。
func (s *Store) GetModelRoute(ctx context.Context, workspaceID, routeID uuid.UUID) (db.WorkspaceModelRoute, error) {
	routes, err := s.queries.ListWorkspaceModelRoutes(ctx, workspaceID)
	if err != nil {
		return db.WorkspaceModelRoute{}, err
	}
	for _, r := range routes {
		if r.ID == routeID {
			return r, nil
		}
	}
	return db.WorkspaceModelRoute{}, domain.ErrNotFound
}

