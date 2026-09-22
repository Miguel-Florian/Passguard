package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/entreprise/device-auth/internal/config"
	"github.com/entreprise/device-auth/internal/domain"
	"github.com/entreprise/device-auth/internal/repository"
)

type ScanService struct {
	devices *repository.DeviceRepo
	audits  *repository.AuditRepo
	cfg     *config.Config
}

func NewScanService(d *repository.DeviceRepo, a *repository.AuditRepo, cfg *config.Config) *ScanService {
	return &ScanService{devices: d, audits: a, cfg: cfg}
}

func (s *ScanService) Options() FluxOptions {
	return FluxOptions{
		PivotHour:         s.cfg.PivotHour,
		DerogationMaxHour: s.cfg.DerogationMaxHour,
		Loc:               s.cfg.Loc,
	}
}

// Preview évalue un device SANS rien écrire : c'est ce que sert GET /v/:sn.
// Aucun log n'est créé, la consultation n'est pas un mouvement.
func (s *ScanService) Preview(ctx context.Context, serial, zone, sens string) (*domain.Device, Evaluation, error) {
	d, err := s.devices.GetBySerial(ctx, serial)
	if err != nil {
		return nil, Evaluation{}, err
	}
	ev := Evaluer(d, ScanInput{
		Sens: sens,
		Zone: zone,
		Now:  s.cfg.Now(),
	}, s.Options())
	return d, ev, nil
}

type RecordParams struct {
	Serial        string
	Sens          string
	Zone          string
	Derogation    bool
	Justification string
	Agent         *domain.Admin
	IP            string
	UserAgent     string
}

// Record évalue puis enregistre le mouvement de façon atomique :
// une ligne immuable dans audit_logs + mise à jour du cache de flux.
func (s *ScanService) Record(ctx context.Context, p RecordParams) (*domain.Device, Evaluation, *domain.AuditLog, error) {
	d, err := s.devices.GetBySerial(ctx, p.Serial)
	if err != nil {
		return nil, Evaluation{}, nil, err
	}

	now := s.cfg.Now()
	ev := Evaluer(d, ScanInput{
		Sens:          p.Sens,
		Zone:          p.Zone,
		Derogation:    p.Derogation,
		Justification: p.Justification,
		Now:           now,
	}, s.Options())

	snapshot := map[string]any{
		"device":     d,
		"evaluation": ev,
		"version":    1,
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, Evaluation{}, nil, err
	}

	logEntry := &domain.AuditLog{
		DeviceID:       d.ID,
		NumeroSerie:    d.NumeroSerie,
		IDMateriel:     d.IDMateriel,
		NomPrenom:      d.NomPrenom,
		ScannedAt:      now,
		JourneeLogique: ev.JourneeLogique,
		SensSuggere:    ev.SensSuggere,
		SensConfirme:   ev.SensApplique,
		Decision:       ev.Decision,
		AccessGranted:  ev.AccessGranted,
		Motifs:         ev.Motifs,
		Derogation:     ev.DerogationAppliquee,
		Justification:  strings.TrimSpace(p.Justification),
		PointControle:  s.cfg.PointControle,
		Zone:           p.Zone,
		IPAddress:      p.IP,
		UserAgent:      p.UserAgent,
		Snapshot:       raw,
	}
	if p.Agent != nil {
		id := p.Agent.ID
		logEntry.AgentID = &id
		logEntry.AgentNom = p.Agent.NomPrenom
	}

	// Le statut de flux ne bascule que si le mouvement a réellement eu lieu.
	majFlux := ev.AccessGranted

	if _, err := s.devices.RecordScan(ctx, logEntry, majFlux); err != nil {
		return nil, Evaluation{}, nil, err
	}

	// On renvoie la fiche rafraîchie pour que l'UI affiche le nouvel état.
	refreshed, err := s.devices.GetBySerial(ctx, d.NumeroSerie)
	if err == nil {
		d = refreshed
	}
	return d, ev, logEntry, nil
}

// Historique renvoie les derniers mouvements d'un device.
func (s *ScanService) Historique(ctx context.Context, d *domain.Device, limit int) ([]*domain.AuditLog, error) {
	logs, _, err := s.audits.List(ctx, repository.AuditFilter{DeviceID: &d.ID, Limit: limit})
	return logs, err
}

// JourneeCourante expose la journée logique en cours (tableau de bord).
func (s *ScanService) JourneeCourante() time.Time {
	return JourneeLogique(s.cfg.Now(), s.cfg.PivotHour, s.cfg.Loc)
}
