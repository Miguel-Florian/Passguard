package handler

import (
	"net/http"

	"github.com/entreprise/device-auth/internal/middleware"
	"github.com/gin-gonic/gin"
)

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func (h *Handler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email et mot de passe requis"})
		return
	}

	token, admin, err := h.auth.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		erreurHTTP(c, err)
		return
	}

	// Cookie HttpOnly : le jeton n'est jamais accessible au JavaScript.
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(middleware.CookieName, token,
		int(h.cfg.JWTExpiry.Seconds()), "/", "", h.cfg.IsProduction(), true)

	redirection := "/scan"
	if admin.IsAdmin() {
		redirection = "/admin"
	}
	c.JSON(http.StatusOK, gin.H{
		"token":       token,
		"admin":       admin,
		"redirection": redirection,
	})
}

func (h *Handler) Logout(c *gin.Context) {
	c.SetCookie(middleware.CookieName, "", -1, "/", "", h.cfg.IsProduction(), true)
	c.JSON(http.StatusOK, gin.H{"message": "déconnecté"})
}

func (h *Handler) Me(c *gin.Context) {
	admin := middleware.Current(c)
	if admin == nil {
		c.JSON(http.StatusOK, gin.H{"authenticated": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"authenticated": true, "admin": admin})
}
