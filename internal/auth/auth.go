package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"time"
)

const CookieName = "proxy_hub_session"

type Service struct {
	adminHash         [32]byte
	tokens            *TokenManager
	trustProxyHeaders bool
}

func NewService(adminKey, dataDir string, trustProxyHeaders bool) (*Service, error) {
	tokens, err := NewTokenManager(dataDir, adminKey)
	if err != nil {
		return nil, err
	}
	return &Service{
		adminHash:         sha256.Sum256([]byte(adminKey)),
		tokens:            tokens,
		trustProxyHeaders: trustProxyHeaders,
	}, nil
}

func (s *Service) CheckKey(key string) bool {
	sum := sha256.Sum256([]byte(key))
	return subtle.ConstantTimeCompare(sum[:], s.adminHash[:]) == 1
}

func (s *Service) SetLoginCookie(w http.ResponseWriter, r *http.Request) error {
	value, err := s.tokens.Sign()
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   int((10 * 365 * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecureRequest(r, s.trustProxyHeaders),
	})
	return nil
}

func (s *Service) ClearLoginCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecureRequest(r, s.trustProxyHeaders),
	})
}

func (s *Service) Authenticated(r *http.Request) bool {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return false
	}
	return s.tokens.Verify(cookie.Value)
}

func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Authenticated(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSecureRequest(r *http.Request, trustProxyHeaders bool) bool {
	return r.TLS != nil || (trustProxyHeaders && r.Header.Get("X-Forwarded-Proto") == "https")
}
