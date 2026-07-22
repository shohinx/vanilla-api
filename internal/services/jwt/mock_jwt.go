package jwt

import (
	"errors"
	"time"

	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

type mockTokenService struct{}

func NewMockTokenService() *mockTokenService {
	return &mockTokenService{}
}

func (m *mockTokenService) IssuePair(user models.User, now time.Time) (TokenPair, error) {
	if user.ID == "jwt_generate_tokens_error" || user.ID == "jwt_generate_access_error_token" {
		return TokenPair{}, errors.New("error generating tokens")
	}

	return TokenPair{
		AccessToken:           "accessToken",
		AccessTokenExpiresAt:  now.Add(15 * time.Minute),
		RefreshToken:          "refreshToken",
		RefreshTokenExpiresAt: now.Add(30 * 24 * time.Hour),
	}, nil
}

func (m *mockTokenService) ParseAccessToken(tokenString string) (models.Principal, error) {
	switch tokenString {
	case "jwt_parse_access_error":
		return models.Principal{}, errors.New("error parsing access token")
	case "jwt_invalid_access_token":
		return models.Principal{}, errors.New("invalid token")
	case "jwt_admin_access_token":
		return models.Principal{ID: "user-id", Role: models.RoleAdmin}, nil
	case "jwt_db_update_password_error":
		return models.Principal{ID: "db_update_user_password_error", Role: models.RoleUser}, nil
	default:
		return models.Principal{ID: "user-id", Role: models.RoleUser}, nil
	}
}
