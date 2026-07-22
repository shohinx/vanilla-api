// Package jwt issues and validates access tokens and creates refresh tokens.
package jwt

import (
	"crypto/rand"
	"encoding/base64"
	"errors"

	"time"

	"github.com/shohinx/vanilla-api/internal/sdk/errs"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/shohinx/vanilla-api/internal/config"
	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

const accessTokenType = "access"

type TokenRepository interface {
	IssuePair(user models.User, now time.Time) (TokenPair, error)
	ParseAccessToken(token string) (models.Principal, error)
}

type TokenService struct {
	secret          []byte
	issuer          string
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
}

type Claims struct {
	Role      string `json:"role"`
	TokenType string `json:"typ"`
	jwt.RegisteredClaims
}

type TokenPair struct {
	AccessToken           string
	AccessTokenExpiresAt  time.Time
	RefreshToken          string
	RefreshTokenExpiresAt time.Time
}

func NewTokenService(cfg config.AuthConfig) *TokenService {
	return &TokenService{
		secret:          []byte(cfg.JWTSecret),
		issuer:          cfg.Issuer,
		accessTokenTTL:  cfg.AccessTokenTTL,
		refreshTokenTTL: cfg.RefreshTokenTTL,
	}
}

func (m *TokenService) IssuePair(user models.User, now time.Time) (TokenPair, error) {
	accessToken, accessExpiresAt, err := m.IssueAccessToken(user, now)
	if err != nil {
		return TokenPair{}, err
	}

	refreshToken, err := NewRefreshToken()
	if err != nil {
		return TokenPair{}, err
	}

	return TokenPair{
		AccessToken:           accessToken,
		AccessTokenExpiresAt:  accessExpiresAt,
		RefreshToken:          refreshToken,
		RefreshTokenExpiresAt: now.Add(m.refreshTokenTTL),
	}, nil
}

func (m *TokenService) IssueAccessToken(user models.User, now time.Time) (string, time.Time, error) {
	accessExpiresAt := now.Add(m.accessTokenTTL)
	claims := Claims{
		Role:      roleForUser(user),
		TokenType: accessTokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   user.ID,
			ExpiresAt: jwt.NewNumericDate(accessExpiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}

	accessToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, err
	}

	return accessToken, accessExpiresAt, nil
}

func (m *TokenService) ParseAccessToken(tokenString string) (models.Principal, error) {
	claims := new(Claims)
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return m.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(m.issuer), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		if err == nil {
			err = errors.New("token is invalid")
		}
		return models.Principal{}, errs.Wrap(err, errs.ErrUnauthorized.Status, errs.ErrUnauthorized.Key)
	}
	if claims.TokenType != accessTokenType {
		return models.Principal{}, errs.Wrap(errors.New("token type is not access"), errs.ErrUnauthorized.Status, errs.ErrUnauthorized.Key)
	}
	if claims.Subject == "" {
		return models.Principal{}, errs.Wrap(errors.New("token subject is empty"), errs.ErrUnauthorized.Status, errs.ErrUnauthorized.Key)
	}
	return models.Principal{ID: claims.Subject, Role: roleForClaims(claims)}, nil
}

func roleForUser(user models.User) string {
	if user.Role.Valid() {
		return string(user.Role)
	}
	return string(models.RoleUser)
}

func roleForClaims(claims *Claims) models.Role {
	if role := models.Role(claims.Role); role.Valid() {
		return role
	}
	return models.RoleUser
}

func NewRefreshToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
