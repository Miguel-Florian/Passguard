package handler

import (
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"time"

	"github.com/entreprise/device-auth/internal/config"
	"github.com/entreprise/device-auth/internal/domain"
	"github.com/entreprise/device-auth/internal/middleware"
	"github.com/entreprise/device-auth/internal/repository"
	"github.com/entreprise/device-auth/internal/service"
	"github.com/gin-gonic/gin"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Handler struct {
	cfg     *config.Config
	auth    *service.AuthService
	scan    *service.ScanService
	imports *service.ImportService
	qr      *service.QRService
	devices *repository.DeviceRepo
	audits  *repository.AuditRepo
}

func New(
	cfg *config.Config,
	auth *service.AuthService,
	scan *service.ScanService,
	imports *service.ImportService,
	qr *service.QRService,
	devices *repository.DeviceRepo,
	audits *repository.AuditRepo,
) *Handler {
	return &Handler{cfg: cfg, auth: auth, scan: scan, imports: imports,
		qr: qr, devices: devices, audits: audits}
}

// Router construit l'ensemble des routes de l'application.
func (h *Handler) Router() *gin.Engine {
	if h.cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	tmpl := template.Must(template.New("").Funcs(templateFuncs(h.cfg)).ParseFS(templatesFS, "templates/*.html"))
	r.SetHTMLTemplate(tmpl)
	r.StaticFS("/static", http.FS(mustSub(staticFS, "static")))

	limiter := middleware.NewRateLimiter(30, 60) // 30 en rafale, 60/minute
	optionnel := middleware.Auth(h.auth, false)
	requis := middleware.Auth(h.auth, true)

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "time": h.cfg.Now()})
	})

	// ---------------- Pages -----------------------------------------
	r.GET("/", optionnel, h.PageAccueil)
	r.GET("/login", optionnel, h.PageLogin)
	r.GET("/scan", requis, h.PageScan)
	// Connexion obligatoire : ouvrir le lien d'un QR code sans être connecté
	// redirige vers /login (qui revient ensuite sur cette même fiche). Le
	// paramètre PUBLIC_VERIFICATION ne joue donc plus que sur l'API JSON
	// /api/verify/:sn ci-dessous, pas sur cette page.
	r.GET("/v/:sn", requis, limiter.Middleware(), h.PageVerification)
	r.GET("/admin", requis, middleware.RequireRole(domain.RoleAdmin), h.PageAdmin)
	r.GET("/admin/etiquettes", requis, middleware.RequireRole(domain.RoleAdmin), h.PageEtiquettes)

	// ---------------- API publique / vigile --------------------------
	api := r.Group("/api")
	{
		api.POST("/auth/login", h.Login)
		api.POST("/auth/logout", h.Logout)
		api.GET("/auth/me", optionnel, h.Me)

		// Consultation : n'écrit jamais dans audit_logs.
		api.GET("/verify/:sn", optionnel, limiter.Middleware(), h.Verify)

		// Enregistrement d'un mouvement : toujours authentifié.
		api.POST("/scan/:sn", requis, middleware.RequireRole(domain.RoleVigile, domain.RoleAdmin), h.Scan)
	}

	// ---------------- API d'administration ---------------------------
	adm := r.Group("/api", requis, middleware.RequireRole(domain.RoleAdmin))
	{
		adm.GET("/stats", h.Stats)

		adm.GET("/devices", h.ListDevices)
		adm.POST("/devices", h.CreateDevice)
		adm.GET("/devices/:id", h.GetDevice)
		adm.PATCH("/devices/:id", h.UpdateDevice)
		adm.DELETE("/devices/:id", h.DeleteDevice)
		adm.GET("/devices/:id/qrcode.png", h.QRCode)
		adm.POST("/qrcodes/regenerate", h.RegenerateQRCodes)

		adm.POST("/import/devices", h.ImportDevices)
		adm.GET("/import/template.xlsx", h.ImportTemplate)
		adm.GET("/import/history", h.ImportHistory)

		adm.GET("/export/devices.xlsx", h.ExportDevices)
		adm.GET("/export/audit.xlsx", h.ExportAudit)

		adm.GET("/audit", h.ListAudit)
	}

	return r
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

// mustSub isole le sous-dossier embarqué pour que /static/app.css
// corresponde bien à static/app.css et non à static/static/app.css.
func mustSub(efs embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(efs, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

func templateFuncs(cfg *config.Config) template.FuncMap {
	return template.FuncMap{
		"date": func(t *time.Time) string {
			if t == nil {
				return "—"
			}
			return t.In(cfg.Loc).Format("02/01/2006")
		},
		"datetime": func(t *time.Time) string {
			if t == nil {
				return "—"
			}
			return t.In(cfg.Loc).Format("02/01/2006 15:04")
		},
		"dt": func(t time.Time) string {
			return t.In(cfg.Loc).Format("02/01/2006 15:04:05")
		},
		"defaut": func(s, def string) string {
			if s == "" {
				return def
			}
			return s
		},
		"ouinon": func(b bool) string {
			if b {
				return "OUI"
			}
			return "NON"
		},
	}
}

// erreurHTTP traduit une erreur métier en réponse JSON normalisée.
func erreurHTTP(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "ressource introuvable"})
	case errors.Is(err, domain.ErrDuplicate):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrValidation):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidCredentials):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "identifiants invalides"})
	case errors.Is(err, domain.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "erreur interne du serveur"})
	}
}
