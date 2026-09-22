package middleware

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/entreprise/device-auth/internal/domain"
	"github.com/entreprise/device-auth/internal/service"
	"github.com/gin-gonic/gin"
)

// CookieName est le nom du cookie portant le jeton.
const CookieName = "pg_token"

// ContextAdmin est la clé du compte authentifié dans le contexte Gin.
const ContextAdmin = "admin"

// Auth extrait et valide le jeton. Si required vaut false, la requête
// continue sans compte (mode consultation publique).
func Auth(auth *service.AuthService, required bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extraireToken(c)
		if token != "" {
			if admin, err := auth.Verify(c.Request.Context(), token); err == nil {
				c.Set(ContextAdmin, admin)
				c.Next()
				return
			}
			// Jeton invalide ou expiré : on nettoie le cookie.
			c.SetCookie(CookieName, "", -1, "/", "", false, true)
		}
		if required {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") {
				c.AbortWithStatusJSON(http.StatusUnauthorized,
					gin.H{"error": "authentification requise"})
				return
			}
			c.Redirect(http.StatusFound, "/login?next="+c.Request.URL.Path)
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireRole exige un des rôles donnés (à placer après Auth).
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		admin := Current(c)
		if admin == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				gin.H{"error": "authentification requise"})
			return
		}
		for _, r := range roles {
			if admin.Role == r {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden,
			gin.H{"error": "droits insuffisants pour cette action"})
	}
}

// Current renvoie le compte authentifié, ou nil.
func Current(c *gin.Context) *domain.Admin {
	v, ok := c.Get(ContextAdmin)
	if !ok {
		return nil
	}
	admin, ok := v.(*domain.Admin)
	if !ok {
		return nil
	}
	return admin
}

func extraireToken(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if ck, err := c.Cookie(CookieName); err == nil {
		return ck
	}
	return ""
}

// ---------------------------------------------------------------------
// Limitation de débit (protège la page publique /v/:sn de l'énumération)
// ---------------------------------------------------------------------

type bucket struct {
	jetons  float64
	dernier time.Time
}

type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	capacite float64
	recharge float64 // jetons par seconde
}

func NewRateLimiter(capacite int, parMinute int) *RateLimiter {
	rl := &RateLimiter{
		buckets:  map[string]*bucket{},
		capacite: float64(capacite),
		recharge: float64(parMinute) / 60.0,
	}
	go rl.nettoyage()
	return rl
}

func (rl *RateLimiter) nettoyage() {
	for range time.Tick(5 * time.Minute) {
		rl.mu.Lock()
		for k, b := range rl.buckets {
			if time.Since(b.dernier) > 15*time.Minute {
				delete(rl.buckets, k)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *RateLimiter) autoriser(cle string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[cle]
	if !ok {
		rl.buckets[cle] = &bucket{jetons: rl.capacite - 1, dernier: now}
		return true
	}
	b.jetons += now.Sub(b.dernier).Seconds() * rl.recharge
	if b.jetons > rl.capacite {
		b.jetons = rl.capacite
	}
	b.dernier = now
	if b.jetons < 1 {
		return false
	}
	b.jetons--
	return true
}

// Middleware applique la limitation, sauf aux comptes authentifiés.
func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if Current(c) != nil {
			c.Next()
			return
		}
		if !rl.autoriser(c.ClientIP()) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests,
				gin.H{"error": "trop de requêtes, réessayez dans un instant"})
			return
		}
		c.Next()
	}
}
