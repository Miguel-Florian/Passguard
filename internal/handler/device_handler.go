package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/entreprise/device-auth/internal/domain"
	"github.com/entreprise/device-auth/internal/middleware"
	"github.com/entreprise/device-auth/internal/repository"
	"github.com/entreprise/device-auth/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------
// Devices
// ---------------------------------------------------------------------

func (h *Handler) ListDevices(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	devices, total, err := h.devices.List(c.Request.Context(), repository.DeviceFilter{
		Search:             c.Query("q"),
		StatutAutorisation: c.Query("statut_autorisation"),
		StatutMateriel:     c.Query("statut_materiel"),
		Flux:               c.Query("flux"),
		Limit:              limit,
		Offset:             offset,
	})
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"total": total, "devices": devices})
}

func (h *Handler) GetDevice(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "identifiant invalide"})
		return
	}
	device, err := h.devices.GetByID(c.Request.Context(), id)
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	logs, _, err := h.audits.List(c.Request.Context(),
		repository.AuditFilter{DeviceID: &id, Limit: 50})
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"device":     device,
		"historique": logs,
		"qr_url":     h.qr.URLVerification(device.NumeroSerie),
	})
}

type deviceRequest struct {
	IDMateriel          string   `json:"id_materiel"`
	NumeroSerie         string   `json:"numero_serie"`
	TypeMateriel        string   `json:"type_materiel"`
	MarqueModele        string   `json:"marque_modele"`
	Matricule           string   `json:"matricule"`
	NomPrenom           string   `json:"nom_prenom"`
	Departement         string   `json:"departement"`
	StatutAutorisation  string   `json:"statut_autorisation"`
	DateExpiration      string   `json:"date_expiration_autorisation"`
	StatutMateriel      string   `json:"statut_materiel"`
	SiteOrigine         string   `json:"site_origine"`
	Zones               []string `json:"zones"`
	HeureAutoriseeDebut string   `json:"heure_autorisee_debut"`
	HeureAutoriseeFin   string   `json:"heure_autorisee_fin"`
	AutoriseWeekend     bool     `json:"autorise_weekend"`
	Commentaire         string   `json:"commentaire"`
}

func (r *deviceRequest) versDomaine(loc *time.Location) (*domain.Device, error) {
	serie := strings.ToUpper(strings.TrimSpace(r.NumeroSerie))
	if serie == "" {
		return nil, fmt.Errorf("%w: le numéro de série est obligatoire", domain.ErrValidation)
	}
	if strings.TrimSpace(r.NomPrenom) == "" {
		return nil, fmt.Errorf("%w: le détenteur est obligatoire", domain.ErrValidation)
	}

	statutAuth := strings.ToUpper(strings.TrimSpace(r.StatutAutorisation))
	if statutAuth == "" {
		statutAuth = domain.AutorisationNonAutorisee
	}
	if !domain.ValidStatutAutorisation(statutAuth) {
		return nil, fmt.Errorf("%w: statut d'autorisation inconnu", domain.ErrValidation)
	}

	statutMat := strings.ToUpper(strings.TrimSpace(r.StatutMateriel))
	if statutMat == "" {
		statutMat = domain.MaterielActif
	}
	if !domain.ValidStatutMateriel(statutMat) {
		return nil, fmt.Errorf("%w: statut matériel inconnu", domain.ErrValidation)
	}

	var exp *time.Time
	if v := strings.TrimSpace(r.DateExpiration); v != "" {
		t, err := time.ParseInLocation("2006-01-02", v, loc)
		if err != nil {
			return nil, fmt.Errorf("%w: date d'expiration invalide (AAAA-MM-JJ attendu)", domain.ErrValidation)
		}
		exp = &t
	}
	if statutAuth == domain.AutorisationTemporaire && exp == nil {
		return nil, fmt.Errorf("%w: une autorisation temporaire exige une date d'expiration", domain.ErrValidation)
	}

	debut := defautStr(r.HeureAutoriseeDebut, "07:00")
	fin := defautStr(r.HeureAutoriseeFin, "18:00")
	if !heureValide(debut) || !heureValide(fin) {
		return nil, fmt.Errorf("%w: heures attendues au format HH:MM", domain.ErrValidation)
	}

	zones := []string{}
	for _, z := range r.Zones {
		if z = strings.TrimSpace(z); z != "" {
			zones = append(zones, z)
		}
	}
	if len(zones) == 0 {
		zones = []string{domain.ZoneAucune}
	}

	idMat := strings.TrimSpace(r.IDMateriel)
	if idMat == "" {
		idMat = serie
	}

	return &domain.Device{
		IDMateriel:                 idMat,
		NumeroSerie:                serie,
		TypeMateriel:               defautStr(r.TypeMateriel, "Non précisé"),
		MarqueModele:               strings.TrimSpace(r.MarqueModele),
		Matricule:                  strings.TrimSpace(r.Matricule),
		NomPrenom:                  strings.TrimSpace(r.NomPrenom),
		Departement:                strings.TrimSpace(r.Departement),
		StatutAutorisation:         statutAuth,
		DateExpirationAutorisation: exp,
		StatutMateriel:             statutMat,
		SiteOrigine:                strings.TrimSpace(r.SiteOrigine),
		Zones:                      zones,
		HeureAutoriseeDebut:        debut,
		HeureAutoriseeFin:          fin,
		AutoriseWeekend:            r.AutoriseWeekend,
		Commentaire:                strings.TrimSpace(r.Commentaire),
	}, nil
}

func (h *Handler) CreateDevice(c *gin.Context) {
	var req deviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "corps de requête invalide"})
		return
	}
	device, err := req.versDomaine(h.cfg.Loc)
	if err != nil {
		erreurHTTP(c, err)
		return
	}

	// Un S/N déjà présent est une erreur explicite : on ne veut pas écraser
	// silencieusement une fiche existante depuis le formulaire unitaire.
	if _, err := h.devices.GetBySerial(c.Request.Context(), device.NumeroSerie); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "ce numéro de série existe déjà"})
		return
	}

	if _, err := h.devices.Upsert(c.Request.Context(), device); err != nil {
		erreurHTTP(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"device": device,
		"qr_url": h.qr.URLVerification(device.NumeroSerie),
	})
}

func (h *Handler) UpdateDevice(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "identifiant invalide"})
		return
	}
	var req deviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "corps de requête invalide"})
		return
	}
	device, err := req.versDomaine(h.cfg.Loc)
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	device.ID = id
	if err := h.devices.Update(c.Request.Context(), device); err != nil {
		erreurHTTP(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"device": device})
}

func (h *Handler) DeleteDevice(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "identifiant invalide"})
		return
	}
	if err := h.devices.SoftDelete(c.Request.Context(), id); err != nil {
		erreurHTTP(c, err)
		return
	}
	// La fiche est archivée, jamais supprimée : l'historique reste intact.
	c.JSON(http.StatusOK, gin.H{"message": "fiche archivée"})
}

func (h *Handler) QRCode(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "identifiant invalide"})
		return
	}
	device, err := h.devices.GetByID(c.Request.Context(), id)
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	taille, _ := strconv.Atoi(c.DefaultQuery("taille", "320"))
	png, err := h.qr.PNG(device.NumeroSerie, taille)
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	c.Header("Content-Disposition",
		fmt.Sprintf(`inline; filename="qr-%s.png"`, device.NumeroSerie))
	c.Data(http.StatusOK, "image/png", png)
}

// RegenerateQRCodes régénère tous les QR codes sur disque, dans le dossier
// interne configuré par QR_CODES_DIR. Ce dossier n'est jamais exposé au
// navigateur : c'est un stockage serveur, déclenché uniquement ici.
func (h *Handler) RegenerateQRCodes(c *gin.Context) {
	devices, err := h.devices.All(c.Request.Context())
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	res, err := h.qr.RegenerateAll(devices)
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"resultat": res})
}

// ---------------------------------------------------------------------
// Import / export
// ---------------------------------------------------------------------

func (h *Handler) ImportDevices(c *gin.Context) {
	fichier, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "aucun fichier reçu (champ 'file')"})
		return
	}
	if fichier.Size > 10<<20 {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "fichier trop volumineux (10 Mo max)"})
		return
	}
	nom := strings.ToLower(fichier.Filename)
	if !strings.HasSuffix(nom, ".xlsx") && !strings.HasSuffix(nom, ".xlsm") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "format attendu : .xlsx"})
		return
	}

	f, err := fichier.Open()
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	defer f.Close()

	var adminID *uuid.UUID
	if a := middleware.Current(c); a != nil {
		id := a.ID
		adminID = &id
	}

	rapport, err := h.imports.Import(c.Request.Context(), f, fichier.Filename, adminID)
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"rapport": rapport})
}

func (h *Handler) ImportTemplate(c *gin.Context) {
	data, err := h.imports.Modele()
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	envoyerClasseur(c, "modele_import_devices.xlsx", data)
}

func (h *Handler) ImportHistory(c *gin.Context) {
	rapports, err := h.audits.ListImports(c.Request.Context(), 20)
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"imports": rapports})
}

func (h *Handler) ExportDevices(c *gin.Context) {
	devices, err := h.devices.All(c.Request.Context())
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	data, err := service.ExportDevices(devices, h.cfg.Loc)
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	envoyerClasseur(c, "devices.xlsx", data)
}

func (h *Handler) ExportAudit(c *gin.Context) {
	logs, _, err := h.audits.List(c.Request.Context(), filtreAudit(c, 1000))
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	data, err := service.ExportAudit(logs, h.cfg.Loc)
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	envoyerClasseur(c, "audit_mouvements.xlsx", data)
}

// ---------------------------------------------------------------------
// Audit & statistiques
// ---------------------------------------------------------------------

func (h *Handler) ListAudit(c *gin.Context) {
	logs, total, err := h.audits.List(c.Request.Context(), filtreAudit(c, 100))
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"total": total, "logs": logs})
}

func (h *Handler) Stats(c *gin.Context) {
	stats, err := h.devices.Stats(c.Request.Context(), h.scan.JourneeCourante())
	if err != nil {
		erreurHTTP(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"stats":           stats,
		"journee_logique": h.scan.JourneeCourante().Format("2006-01-02"),
	})
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

func filtreAudit(c *gin.Context, defautLimit int) repository.AuditFilter {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(defautLimit)))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	f := repository.AuditFilter{
		NumeroSerie: c.Query("serie"),
		Decision:    c.Query("decision"),
		Derogation:  c.Query("derogation") == "true",
		Limit:       limit,
		Offset:      offset,
	}
	if v := c.Query("device"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			f.DeviceID = &id
		}
	}
	if v := c.Query("du"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			f.Du = &t
		}
	}
	if v := c.Query("au"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			fin := t.Add(24*time.Hour - time.Second)
			f.Au = &fin
		}
	}
	return f
}

func envoyerClasseur(c *gin.Context, nom string, data []byte) {
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, nom))
	c.Data(http.StatusOK,
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", data)
}

func defautStr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return strings.TrimSpace(v)
}

func heureValide(v string) bool {
	_, err := time.Parse("15:04", v)
	return err == nil
}
