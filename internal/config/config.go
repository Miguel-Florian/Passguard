package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config regroupe toute la configuration de l'application.
// Tout provient de l'environnement (fichier .env en développement).
type Config struct {
	AppEnv  string // development | production
	Port    string
	BaseURL string // utilisé pour générer les URL encodées dans les QR codes

	DatabaseURL string

	JWTSecret string
	JWTExpiry time.Duration

	// Compte administrateur créé automatiquement au démarrage.
	AdminEmail    string
	AdminPassword string
	AdminName     string

	// Compte vigile optionnel créé automatiquement au démarrage.
	VigileEmail    string
	VigilePassword string
	VigileName     string

	// Si true, la page /v/:sn est consultable sans authentification (mode dev).
	// L'écriture (POST /api/scan/:sn) reste TOUJOURS authentifiée.
	PublicVerification bool

	// Heure pivot de la "journée logique" (par défaut 5 => 05:00).
	PivotHour int

	// Heure maximale jusqu'à laquelle un vigile peut accorder une dérogation.
	DerogationMaxHour int

	PointControle string

	// Dossier disque où la console d'administration écrit les fichiers PNG
	// lors d'un clic sur "Régénérer tous les QR codes". Purement interne au
	// serveur : ce dossier n'est jamais servi en statique.
	QRCodesDir string

	TimeZone string
	Loc      *time.Location
}

func Load() (*Config, error) {
	// .env est optionnel : en production les variables viennent de l'environnement.
	_ = godotenv.Load()

	c := &Config{
		AppEnv:             getEnv("APP_ENV", "development"),
		Port:               getEnv("PORT", "8080"),
		BaseURL:            strings.TrimRight(getEnv("SERVER_BASE_URL", "http://localhost:8080"), "/"),
		DatabaseURL:        getEnv("DATABASE_URL", ""),
		JWTSecret:          getEnv("JWT_SECRET", ""),
		JWTExpiry:          time.Duration(getEnvInt("JWT_EXPIRY_HOURS", 12)) * time.Hour,
		AdminEmail:         strings.ToLower(strings.TrimSpace(getEnv("ADMIN_EMAIL", ""))),
		AdminPassword:      getEnv("ADMIN_PASSWORD", ""),
		AdminName:          getEnv("ADMIN_NAME", "Administrateur"),
		VigileEmail:        strings.ToLower(strings.TrimSpace(getEnv("VIGILE_EMAIL", ""))),
		VigilePassword:     getEnv("VIGILE_PASSWORD", ""),
		VigileName:         getEnv("VIGILE_NAME", "Vigile Guérite"),
		PublicVerification: getEnvBool("PUBLIC_VERIFICATION", true),
		PivotHour:          getEnvInt("PIVOT_HOUR", 5),
		DerogationMaxHour:  getEnvInt("DEROGATION_MAX_HOUR", 21),
		PointControle:      getEnv("POINT_CONTROLE", "Guérite Principale"),
		QRCodesDir:         getEnv("QR_CODES_DIR", "../qr_codes"),
		TimeZone:           getEnv("TZ", "Africa/Douala"),
	}

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL est obligatoire")
	}
	if c.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET est obligatoire (32 caractères minimum)")
	}
	if c.IsProduction() && len(c.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET doit faire au moins 32 caractères en production")
	}
	if c.AdminEmail == "" || c.AdminPassword == "" {
		return nil, fmt.Errorf("ADMIN_EMAIL et ADMIN_PASSWORD sont obligatoires (compte créé au démarrage)")
	}
	if c.PivotHour < 0 || c.PivotHour > 23 {
		return nil, fmt.Errorf("PIVOT_HOUR doit être compris entre 0 et 23")
	}

	loc, err := time.LoadLocation(c.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("fuseau horaire invalide (%s): %w", c.TimeZone, err)
	}
	c.Loc = loc

	return c, nil
}

func (c *Config) IsProduction() bool {
	return strings.EqualFold(c.AppEnv, "production")
}

// Now renvoie l'heure courante dans le fuseau configuré.
func (c *Config) Now() time.Time {
	return time.Now().In(c.Loc)
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return def
}
