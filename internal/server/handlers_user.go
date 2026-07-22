package server

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/shohinx/vanilla-api/internal/sdk/middleware"
	"github.com/shohinx/vanilla-api/internal/sdk/models"
	"github.com/shohinx/vanilla-api/internal/sdk/sqldb"
)

func (s *Server) HandleMe(c *gin.Context) {
	userID, err := middleware.GetUserID(c)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "unauthorized", nil)
		return
	}
	user, err := s.db.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			writeError(c, http.StatusNotFound, "user_not_found", nil)
			return
		}
		writeError(c, http.StatusInternalServerError, "internal_get_user_error", nil)
		return
	}
	c.JSON(http.StatusOK, user)
}

func (s *Server) HandleListUsers(c *gin.Context) {
	users, err := s.db.ListUsers(c.Request.Context())
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_list_users_error", nil)
		return
	}
	c.JSON(http.StatusOK, users)
}

func (s *Server) HandleGrantAdminRole(c *gin.Context) {
	user, err := s.db.PromoteUserToAdmin(c.Request.Context(), c.Param("user_id"))
	if err != nil {
		writeUserMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, user)
}

func (s *Server) HandleRevokeAdminRole(c *gin.Context) {
	user, err := s.db.DemoteUserFromAdmin(c.Request.Context(), c.Param("user_id"))
	if err != nil {
		writeUserMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, user)
}

func (s *Server) HandlePasswordChange(c *gin.Context) {
	var request struct {
		CurrentPassword string `json:"current_password"`
		Password        string `json:"password"`
		PasswordConfirm string `json:"password_confirm"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	if request.Password != request.PasswordConfirm {
		writeError(c, http.StatusBadRequest, "password_mismatch", nil)
		return
	}
	if err := validatePassword(request.Password); err != nil {
		writeError(c, http.StatusBadRequest, err.Error(), nil)
		return
	}
	userID, err := middleware.GetUserID(c)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "unauthorized", nil)
		return
	}
	user, err := s.db.GetUserByID(c.Request.Context(), userID)
	if err != nil || bcrypt.CompareHashAndPassword(user.Password, []byte(request.CurrentPassword)) != nil {
		writeError(c, http.StatusUnauthorized, "invalid_credentials", nil)
		return
	}
	hashedPassword, err := generateFromPassword([]byte(request.Password), bcryptCost)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_hash_error", nil)
		return
	}
	if err := s.db.UpdateUserPasswordAndRevokeTokens(c.Request.Context(), userID, hashedPassword); err != nil {
		writeUserMutationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) HandleUpdateUsername(c *gin.Context) {
	var update models.UpdateUsername
	if err := c.ShouldBindJSON(&update); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	if update.Username == "" {
		writeError(c, http.StatusBadRequest, "username_required", nil)
		return
	}
	userID, err := middleware.GetUserID(c)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "unauthorized", nil)
		return
	}
	user, err := s.db.UpdateUsername(c.Request.Context(), userID, update)
	if err != nil {
		writeUsernameMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, user)
}

func writeUsernameMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sqldb.ErrDBNotFound):
		writeError(c, http.StatusNotFound, "user_not_found", nil)
	case errors.Is(err, sqldb.ErrDBDuplicatedEntry):
		writeError(c, http.StatusConflict, "username_already_exists", nil)
	case errors.Is(err, sqldb.ErrInvalidInput):
		writeError(c, http.StatusBadRequest, "username_required", nil)
	default:
		writeError(c, http.StatusInternalServerError, "internal_update_username_error", nil)
	}
}

func writeUserMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sqldb.ErrDBNotFound):
		writeError(c, http.StatusNotFound, "user_not_found", nil)
	case errors.Is(err, sqldb.ErrDBDuplicatedEntry):
		writeError(c, http.StatusConflict, "user_already_exists", nil)
	default:
		writeError(c, http.StatusInternalServerError, "internal_update_user_error", nil)
	}
}
