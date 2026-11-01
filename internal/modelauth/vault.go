package modelauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

type vault struct {
	aead cipher.AEAD
}

func newVault(encodedKey string) (*vault, []byte, error) {
	key, err := decodeKey(encodedKey)
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("create model credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("create model credential AEAD: %w", err)
	}
	return &vault{aead: aead}, key, nil
}

func decodeKey(value string) ([]byte, error) {
	var key []byte
	var err error
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		key, err = encoding.DecodeString(value)
		if err == nil {
			break
		}
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("CYCLEDOCK_MODEL_SECRET_KEY must be a base64-encoded 32-byte key")
	}
	return key, nil
}

func (v *vault) seal(value any, aad string) ([]byte, error) {
	plaintext, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode model credential payload: %w", err)
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate model credential nonce: %w", err)
	}
	return v.aead.Seal(nonce, nonce, plaintext, []byte(aad)), nil
}

func (v *vault) open(ciphertext []byte, aad string, destination any) error {
	nonceSize := v.aead.NonceSize()
	if len(ciphertext) <= nonceSize {
		return errors.New("invalid encrypted model credential")
	}
	plaintext, err := v.aead.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], []byte(aad))
	if err != nil {
		return errors.New("decrypt model credential")
	}
	if err := json.Unmarshal(plaintext, destination); err != nil {
		return errors.New("decode model credential")
	}
	return nil
}
