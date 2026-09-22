package handler

import (
	"net/http"
	"strings"

	"github.com/entreprise/device-auth/internal/domain"
	"github.com/entreprise/device-auth/internal/middleware"
	"github.com/entreprise/device-auth/internal/service"
	"github.com/gin-gonic/gin"
)

// Verify : consultation pure (aucune écriture dans audit_logs).
// GET /api/verify/:sn
func (h *Handler) Verify(c *gin.Context) {
	sn := strings.TrimSpace(c.Param("sn"))
	sens := c.Query("sens")
	zone := c.Query("zone")

	device, ev, err := h.scan.Preview(c.Request.Context(), sn, zone, sens)
	if err != nil {
		erreurHTTP(c, err)
		return
	}

	authentifie := middleware.Current(c) != nil
	if !authentifie && !h.cfg.PublicVerification {
		// Mode production : vue minimale, sans données nominatives.
		c.JSON(http.StatusOK, gin.H{
			"restreint":      true,
			"numero_serie":   device.NumeroSerie,
			"type_materiel":  device.TypeMateriel,
			"access_granted": ev.AccessGranted,
			"decision":       ev.Decision,
			"title":          ev.Title,
			"color":          ev.Color,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"restreint":  false,
		"device":     device,
		"evaluation": ev,
	})
}

type scanRequest struct {
	Sens          string `json:"sens"`
	Zone          string `json:"zone"`
	Derogation    bool   `json:"derogation"`
	Justification string `json:"justification"`
}

// Scan : enregistre réellement le mouvement.
// POST /api/scan/:sn  (VIGILE ou ADMIN)
func (h *Handler) Scan(c *gin.Context) {
	sn := strings.TrimSpace(c.Param("sn"))

	var req scanRequest
	// Le corps est optionnel : sans sens précisé, le serveur applique
	// le sens suggéré par alternance.
	_ = c.ShouldBindJSON(&req)

	if req.Derogation && strings.TrimSpace(req.Justification) == "" {
		c.JSON(http.StatusBadRequest,
			gin.H{"error": "une dérogation exige une justification écrite"})
		return
	}

	agent := middleware.Current(c)
	device, ev, logEntry, err := h.scan.Record(c.Request.Context(), serviceRecordParams(sn, req, agent, c))
	if err != nil {
		erreurHTTP(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"device":     device,
		"evaluation": ev,
		"log_id":     logEntry.ID,
		"scanned_at": logEntry.ScannedAt,
	})
}

// serviceRecordParams assemble les paramètres du scan à partir de la requête HTTP.
func serviceRecordParams(sn string, req scanRequest, agent *domain.Admin, c *gin.Context) service.RecordParams {
	return service.RecordParams{
		Serial:        sn,
		Sens:          strings.ToUpper(strings.TrimSpace(req.Sens)),
		Zone:          strings.TrimSpace(req.Zone),
		Derogation:    req.Derogation,
		Justification: strings.TrimSpace(req.Justification),
		Agent:         agent,
		IP:            c.ClientIP(),
		UserAgent:     c.Request.UserAgent(),
	}
}
