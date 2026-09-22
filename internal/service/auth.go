package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/entreprise/device-auth/internal/config"
	"github.com/entreprise/device-auth/internal/domain"
	"github.com/entreprise/device-auth/internal/repository"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type AuthService struct {
	admins *repository.AdminRepo
	cfg    *config.Config
}

func NewAuthService(admins *repository.AdminRepo, cfg *config.Config) *AuthService {
	return &AuthService{admins: admins, cfg: cfg}
}

type Claims struct {
	Role      string `json:"role"`
	NomPrenom string `json:"nom"`
	jwt.RegisteredClaims
}

func HashPassword(plain string) (string, error) {
	if len(plain) < 8 {
		return "", fmt.Errorf("%w: mot de passe trop court (8 caractères minimum)", domain.ErrValidation)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(h), err
}

func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// Login vérifie les identifiants et renvoie un jeton signé.
func (s *AuthService) Login(ctx context.Context, email, password string) (string, *domain.Admin, error) {
	admin, err := s.admins.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Coût constant : on ne révèle pas si l'email existe.
			_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinvalidinv"), []byte(password))
			return "", nil, domain.ErrInvalidCredentials
		}
		return "", nil, err
	}
	if !admin.Actif {
		return "", nil, fmt.Errorf("%w: compte désactivé", domain.ErrForbidden)
	}
	if !CheckPassword(admin.MotDePasse, password) {
		return "", nil, domain.ErrInvalidCredentials
	}

	token, err := s.GenerateToken(admin)
	if err != nil {
		return "", nil, err
	}
	_ = s.admins.TouchLogin(ctx, admin.ID)
	return token, admin, nil
}

func (s *AuthService) GenerateToken(a *domain.Admin) (string, error) {
	now := time.Now()
	claims := Claims{
		Role:      a.Role,
		NomPrenom: a.NomPrenom,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   a.ID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.cfg.JWTExpiry)),
			Issuer:    "hardware-passguard",
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.cfg.JWTSecret))
}

// Verify valide le jeton et recharge le compte depuis la base : un compte
// désactivé perd immédiatement l'accès, sans attendre l'expiration du jeton.
func (s *AuthService) Verify(ctx context.Context, token string) (*domain.Admin, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, domain.ErrInvalidCredentials
	}

	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("méthode de signature inattendue: %v", t.Header["alg"])
		}
		return []byte(s.cfg.JWTSecret), nil
	})
	if err != nil || !parsed.Valid {
		return nil, domain.ErrInvalidCredentials
	}

	claims, ok := parsed.Claims.(*Claims)
	if !ok {
		return nil, domain.ErrInvalidCredentials
	}
	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, domain.ErrInvalidCredentials
	}

	admin, err := s.admins.GetByID(ctx, id)
	if err != nil {
		return nil, domain.ErrInvalidCredentials
	}
	if !admin.Actif {
		return nil, domain.ErrForbidden
	}
	return admin, nil
}

// Bootstrap crée les comptes par défaut décrits dans le .env.
func (s *AuthService) Bootstrap(ctx context.Context) error {
	comptes := []struct {
		email, password, nom, role string
	}{
		{s.cfg.AdminEmail, s.cfg.AdminPassword, s.cfg.AdminName, domain.RoleAdmin},
		{s.cfg.VigileEmail, s.cfg.VigilePassword, s.cfg.VigileName, domain.RoleVigile},
	}

	for _, c := range comptes {
		if c.email == "" || c.password == "" {
			continue
		}
		hash, err := HashPassword(c.password)
		if err != nil {
			return fmt.Errorf("compte %s: %w", c.email, err)
		}
		a := &domain.Admin{
			Email:      c.email,
			NomPrenom:  c.nom,
			MotDePasse: hash,
			Role:       c.role,
			Actif:      true,
		}
		created, err := s.admins.EnsureExists(ctx, a)
		if err != nil {
			return fmt.Errorf("création du compte %s: %w", c.email, err)
		}
		if created {
			fmt.Printf("[bootstrap] compte %s créé (%s)\n", c.email, c.role)
		}
	}
	return nil
}
