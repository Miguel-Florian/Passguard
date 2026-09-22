package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/entreprise/device-auth/internal/domain"
	"github.com/entreprise/device-auth/internal/middleware"
	"github.com/entreprise/device-auth/internal/service"
	"github.com/gin-gonic/gin"
)

// PageData est le modèle passé aux templates.
type PageData struct {
	Titre         string
	Admin         *domain.Admin
	Device        *domain.Device
	Eval          *service.Evaluation
	Historique    []*domain.AuditLog
	QRDataURI     string
	Erreur        string
	Serial        string
	PointControle string
	Maintenant    string
	ModePublic    bool
	Restreint     bool
	AppEnv        string
	DerogMaxHeure int
}

func (h *Handler) base(c *gin.Context, titre string) PageData {
	return PageData{
		Titre:         titre,
		Admin:         middleware.Current(c),
		PointControle: h.cfg.PointControle,
		Maintenant:    h.cfg.Now().Format("02/01/2006 15:04"),
		ModePublic:    h.cfg.PublicVerification,
		AppEnv:        h.cfg.AppEnv,
		DerogMaxHeure: h.cfg.DerogationMaxHour,
	}
}

func (h *Handler) PageAccueil(c *gin.Context) {
	admin := middleware.Current(c)
	switch {
	case admin == nil:
		c.Redirect(http.StatusFound, "/login")
	case admin.IsAdmin():
		c.Redirect(http.StatusFound, "/admin")
	default:
		c.Redirect(http.StatusFound, "/scan")
	}
}

func (h *Handler) PageLogin(c *gin.Context) {
	if admin := middleware.Current(c); admin != nil {
		if admin.IsAdmin() {
			c.Redirect(http.StatusFound, "/admin")
		} else {
			c.Redirect(http.StatusFound, "/scan")
		}
		return
	}
	c.HTML(http.StatusOK, "login.html", h.base(c, "Connexion"))
}

// PageScan affiche la guérite sans matériel chargé.
func (h *Handler) PageScan(c *gin.Context) {
	data := h.base(c, "Poste de contrôle")
	c.HTML(http.StatusOK, "verification.html", data)
}

// PageVerification est la cible du QR code. Elle ne crée AUCUN log :
// c'est une consultation. Seul le bouton ENTRÉE/SORTIE enregistre.
func (h *Handler) PageVerification(c *gin.Context) {
	sn := strings.TrimSpace(c.Param("sn"))
	data := h.base(c, "Contrôle "+sn)
	data.Serial = sn

	authentifie := data.Admin != nil
	if !authentifie && !h.cfg.PublicVerification {
		data.Restreint = true
	}

	device, ev, err := h.scan.Preview(c.Request.Context(), sn, c.Query("zone"), c.Query("sens"))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			data.Erreur = "Aucun matériel enregistré avec le numéro de série " + sn +
				". Ne pas laisser sortir l'équipement sans vérification manuelle."
			c.HTML(http.StatusNotFound, "verification.html", data)
			return
		}
		data.Erreur = "Erreur serveur lors de la vérification."
		c.HTML(http.StatusInternalServerError, "verification.html", data)
		return
	}

	data.Device = device
	data.Eval = &ev
	if uri, err := h.qr.DataURI(device.NumeroSerie, 160); err == nil {
		data.QRDataURI = uri
	}
	if authentifie {
		if logs, err := h.scan.Historique(c.Request.Context(), device, 8); err == nil {
			data.Historique = logs
		}
	}
	c.HTML(http.StatusOK, "verification.html", data)
}

func (h *Handler) PageAdmin(c *gin.Context) {
	c.HTML(http.StatusOK, "admin.html", h.base(c, "Administration"))
}

// PageEtiquettes produit la planche de QR codes à imprimer
// (impression navigateur -> PDF, sans dépendance supplémentaire).
func (h *Handler) PageEtiquettes(c *gin.Context) {
	devices, err := h.devices.All(c.Request.Context())
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		filtres := []*domain.Device{}
		for _, d := range devices {
			if strings.Contains(strings.ToUpper(d.NumeroSerie), strings.ToUpper(q)) ||
				strings.Contains(strings.ToUpper(d.IDMateriel), strings.ToUpper(q)) {
				filtres = append(filtres, d)
			}
		}
		devices = filtres
	}

	etiquettes, err := h.qr.Etiquettes(devices)
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	c.HTML(http.StatusOK, "etiquettes.html", gin.H{
		"Titre":      "Planche d'étiquettes",
		"Etiquettes": etiquettes,
		"Total":      len(etiquettes),
	})
}
