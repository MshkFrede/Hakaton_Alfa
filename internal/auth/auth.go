// Package auth — хэширование паролей (PBKDF2-SHA256 из стандартной библиотеки) и JWT-токены.
// В промышленной версии аутентификация выносится во внешний IdP (Keycloak / корпоративный SSO),
// а сервис только проверяет токены — интерфейс Issuer/Parse это допускает.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const iterations = 210_000

// HashPassword возвращает строку вида pbkdf2-sha256$<iter>$<salt>$<hash>.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", iterations, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// CheckPassword сравнивает пароль с хэшем за постоянное время.
func CheckPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// Claims — содержимое токена.
type Claims struct {
	Role      string `json:"role"`
	Name      string `json:"name"`
	CompanyID string `json:"cid,omitempty"`
	jwt.RegisteredClaims
}

type Issuer struct {
	secret []byte
	ttl    time.Duration
}

func NewIssuer(secret string, ttl time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), ttl: ttl}
}

func (i *Issuer) Issue(userID, role, name, companyID string) (string, time.Time, error) {
	exp := time.Now().Add(i.ttl)
	c := Claims{Role: role, Name: name, CompanyID: companyID, RegisteredClaims: jwt.RegisteredClaims{
		Subject:   userID,
		Issuer:    "credit-conveyor",
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(exp),
	}}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(i.secret)
	return tok, exp, err
}

func (i *Issuer) Parse(token string) (*Claims, error) {
	c := &Claims{}
	t, err := jwt.ParseWithClaims(token, c, func(t *jwt.Token) (any, error) {
		return i.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("credit-conveyor"), jwt.WithExpirationRequired())
	if err != nil || !t.Valid {
		return nil, errors.New("invalid token")
	}
	return c, nil
}
