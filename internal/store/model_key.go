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

func (s *Store) encryptAPICredentialKey(workspaceID, credentialID string, key string) ([]byte, error) {
	if s.modelCipher == nil {
		return nil, errors.New("model API key encryption key is not configured (CYCLEDOCK_MODEL_SECRET_KEY)")
	}
	nonce := make([]byte, s.modelCipher.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate model API key nonce: %w", err)
	}
	aad := []byte("cycledock/api_credential/" + workspaceID + "/" + credentialID)
	return s.modelCipher.Seal(nonce, nonce, []byte(key), aad), nil
}

// encryptLegacyRouteModelAPIKey 仅供历史迁移兼容测试模拟 000032 之前的路由级密文格式。
func (s *Store) encryptLegacyRouteModelAPIKey(workspaceID, routeID string, key string) ([]byte, error) {
	if s.modelCipher == nil {
		return nil, errors.New("model API key encryption key is not configured (CYCLEDOCK_MODEL_SECRET_KEY)")
	}
	nonce := make([]byte, s.modelCipher.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate model API key nonce: %w", err)
	}
	aad := []byte("cycledock/model_route/" + workspaceID + "/" + routeID)
	return s.modelCipher.Seal(nonce, nonce, []byte(key), aad), nil
}

func (s *Store) decryptAPICredentialCiphertext(workspaceID, credentialID string, ciphertext []byte) (string, error) {
	if len(ciphertext) == 0 {
		return "", nil
	}
	if s.modelCipher == nil {
		return "", errors.New("model API key encryption key is not configured (CYCLEDOCK_MODEL_SECRET_KEY)")
	}
	nonceSize := s.modelCipher.NonceSize()
	if len(ciphertext) < nonceSize+s.modelCipher.Overhead() {
		return "", errors.New("invalid model API key ciphertext")
	}
	nonce, sealed := ciphertext[:nonceSize], ciphertext[nonceSize:]
	key, err := s.modelCipher.Open(nil, nonce, sealed, []byte("cycledock/api_credential/"+workspaceID+"/"+credentialID))
	if err == nil {
		return string(key), nil
	}
	// 兼容 000032 迁移前保存在 workspace_model_routes 中的历史密文：
	// SQL 迁移沿用了原路由 ID 作为凭据 ID，因此仍受相同 workspace_id + id 的 AEAD 绑定保护。
	legacyKey, legacyErr := s.modelCipher.Open(nil, nonce, sealed, []byte("cycledock/model_route/"+workspaceID+"/"+credentialID))
	if legacyErr == nil {
		return string(legacyKey), nil
	}
	return "", errors.New("model API key decryption failed")
}

// APICredentialKey 解密自定义服务商凭据的 API Key；仅在服务端调用模型、拉取目录或连通性检查时使用，绝不返回前端。
func (s *Store) APICredentialKey(credential db.WorkspaceApiCredential) (string, error) {
	return s.decryptAPICredentialCiphertext(
		credential.WorkspaceID.String(),
		credential.ID.String(),
		credential.ApiKeyCiphertext,
	)
}

// ModelRouteAPIKey 解密模型路由关联的服务商 API Key；仅在服务端使用，绝不返回前端。
func (s *Store) ModelRouteAPIKey(route ModelRoute) (string, error) {
	credentialID := route.ID
	if route.ApiCredentialID.Valid {
		credentialID = route.ApiCredentialID.UUID
	}
	return s.decryptAPICredentialCiphertext(
		route.WorkspaceID.String(),
		credentialID.String(),
		route.ApiKeyCiphertext,
	)
}

// GetModelRoute 根据 Workspace 与 Route ID 获取包含已关联服务商凭据的模型路由。
func (s *Store) GetModelRoute(ctx context.Context, workspaceID, routeID uuid.UUID) (ModelRoute, error) {
	routes, err := s.queries.ListWorkspaceModelRoutes(ctx, workspaceID)
	if err != nil {
		return ModelRoute{}, err
	}
	for _, r := range routes {
		if r.ID == routeID {
			return r, nil
		}
	}
	return ModelRoute{}, domain.ErrNotFound
}
