package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type tokenPayload struct {
	HashPrefix string `json:"hash_prefix"`
	IssuedAt   int64  `json:"iat"`
}

type TokenManager struct {
	secret     []byte
	hashPrefix string
}

func NewTokenManager(dataDir, adminKey string) (*TokenManager, error) {
	secret, err := loadOrCreateSecret(filepath.Join(dataDir, ".secret"))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(adminKey))
	return &TokenManager{
		secret:     secret,
		hashPrefix: base64.RawURLEncoding.EncodeToString(sum[:])[:8],
	}, nil
}

func (m *TokenManager) Sign() (string, error) {
	payload := tokenPayload{
		HashPrefix: m.hashPrefix,
		IssuedAt:   time.Now().Unix(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	bodyText := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(bodyText))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return bodyText + "." + signature, nil
}

func (m *TokenManager) Verify(value string) bool {
	bodyText, sigText, ok := strings.Cut(value, ".")
	if !ok || bodyText == "" || sigText == "" {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigText)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(bodyText))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return false
	}
	body, err := base64.RawURLEncoding.DecodeString(bodyText)
	if err != nil {
		return false
	}
	var payload tokenPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	return payload.HashPrefix == m.hashPrefix
}

func loadOrCreateSecret(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		secret, decErr := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if decErr != nil {
			return nil, decErr
		}
		if len(secret) != 32 {
			return nil, errors.New("secret file must contain 32 random bytes")
		}
		return secret, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	text := []byte(base64.RawStdEncoding.EncodeToString(secret) + "\n")
	if err := atomicWrite(path, text, 0o600); err != nil {
		return nil, err
	}
	return secret, nil
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
