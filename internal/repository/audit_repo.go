package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/entreprise/device-auth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditRepo struct {
	pool *pgxpool.Pool
}

func NewAuditRepo(pool *pgxpool.Pool) *AuditRepo { return &AuditRepo{pool: pool} }

const auditColumns = `
	id, device_id, numero_serie, id_materiel, nom_prenom, scanned_at, journee_logique,
	sens_suggere::text, sens_confirme::text, decision::text, access_granted, motifs,
	derogation, justification, point_controle, zone, agent_id, agent_nom, ip_address, user_agent`

type AuditFilter struct {
	DeviceID    *uuid.UUID
	NumeroSerie string
	Decision    string
	Du          *time.Time
	Au          *time.Time
	Derogation  bool
	Limit       int
	Offset      int
}

func (r *AuditRepo) List(ctx context.Context, f AuditFilter) ([]*domain.AuditLog, int, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}

	where := []string{"1=1"}
	args := []any{}
	add := func(cond string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.DeviceID != nil {
		add(`device_id = $%d`, *f.DeviceID)
	}
	if s := strings.TrimSpace(f.NumeroSerie); s != "" {
		add(`upper(numero_serie) = upper($%d)`, s)
	}
	if f.Decision != "" {
		add(`decision = $%d::decision_scan`, f.Decision)
	}
	if f.Du != nil {
		add(`scanned_at >= $%d`, *f.Du)
	}
	if f.Au != nil {
		add(`scanned_at <= $%d`, *f.Au)
	}
	if f.Derogation {
		where = append(where, "derogation = true")
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_logs WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	q := `SELECT ` + auditColumns + ` FROM audit_logs WHERE ` + clause +
		fmt.Sprintf(` ORDER BY scanned_at DESC, id DESC LIMIT %d OFFSET %d`, f.Limit, f.Offset)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []*domain.AuditLog{}
	for rows.Next() {
		var l domain.AuditLog
		if err := rows.Scan(&l.ID, &l.DeviceID, &l.NumeroSerie, &l.IDMateriel, &l.NomPrenom,
			&l.ScannedAt, &l.JourneeLogique, &l.SensSuggere, &l.SensConfirme, &l.Decision,
			&l.AccessGranted, &l.Motifs, &l.Derogation, &l.Justification, &l.PointControle,
			&l.Zone, &l.AgentID, &l.AgentNom, &l.IPAddress, &l.UserAgent); err != nil {
			return nil, 0, err
		}
		out = append(out, &l)
	}
	return out, total, rows.Err()
}

// SaveImportReport archive le compte rendu d'un import Excel.
func (r *AuditRepo) SaveImportReport(ctx context.Context, adminID *uuid.UUID, rep *domain.ImportReport) error {
	payload, err := json.Marshal(rep.Erreurs)
	if err != nil {
		return err
	}
	return r.pool.QueryRow(ctx, `
		INSERT INTO import_batches (admin_id, nom_fichier, lignes_total, lignes_creees,
		                            lignes_majs, lignes_erreur, rapport)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
		RETURNING id, created_at`,
		adminID, rep.NomFichier, rep.LignesTotal, rep.LignesCreees,
		rep.LignesMajs, rep.LignesErreur, string(payload),
	).Scan(&rep.ID, &rep.CreatedAt)
}

func (r *AuditRepo) ListImports(ctx context.Context, limit int) ([]*domain.ImportReport, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, nom_fichier, lignes_total, lignes_creees, lignes_majs,
		       lignes_erreur, rapport, created_at
		FROM import_batches ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*domain.ImportReport{}
	for rows.Next() {
		var rep domain.ImportReport
		var raw []byte
		if err := rows.Scan(&rep.ID, &rep.NomFichier, &rep.LignesTotal, &rep.LignesCreees,
			&rep.LignesMajs, &rep.LignesErreur, &raw, &rep.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &rep.Erreurs)
		out = append(out, &rep)
	}
	return out, rows.Err()
}
