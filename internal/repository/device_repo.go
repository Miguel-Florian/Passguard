package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/entreprise/device-auth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DeviceRepo struct {
	pool *pgxpool.Pool
}

func NewDeviceRepo(pool *pgxpool.Pool) *DeviceRepo { return &DeviceRepo{pool: pool} }

const deviceColumns = `
	d.id, d.id_materiel, d.numero_serie, d.type_materiel, d.marque_modele,
	COALESCE(d.matricule, ''), d.nom_prenom, d.departement,
	d.statut_autorisation::text, d.date_expiration_autorisation,
	d.statut_materiel::text, d.site_origine,
	to_char(d.heure_autorisee_debut, 'HH24:MI'),
	to_char(d.heure_autorisee_fin, 'HH24:MI'),
	d.autorise_weekend, d.dernier_statut_flux::text, d.horodatage_dernier_scan,
	d.commentaire, d.created_at, d.updated_at,
	COALESCE((SELECT array_agg(z.zone ORDER BY z.zone)
	          FROM device_zones z WHERE z.device_id = d.id), '{}'::text[])`

func scanDevice(row pgx.Row) (*domain.Device, error) {
	var d domain.Device
	err := row.Scan(
		&d.ID, &d.IDMateriel, &d.NumeroSerie, &d.TypeMateriel, &d.MarqueModele,
		&d.Matricule, &d.NomPrenom, &d.Departement,
		&d.StatutAutorisation, &d.DateExpirationAutorisation,
		&d.StatutMateriel, &d.SiteOrigine,
		&d.HeureAutoriseeDebut, &d.HeureAutoriseeFin,
		&d.AutoriseWeekend, &d.DernierStatutFlux, &d.HorodatageDernierScan,
		&d.Commentaire, &d.CreatedAt, &d.UpdatedAt, &d.Zones,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &d, nil
}

func (r *DeviceRepo) GetBySerial(ctx context.Context, serial string) (*domain.Device, error) {
	q := `SELECT ` + deviceColumns + `
	      FROM devices d
	      WHERE d.deleted_at IS NULL AND upper(d.numero_serie) = upper($1)`
	return scanDevice(r.pool.QueryRow(ctx, q, strings.TrimSpace(serial)))
}

func (r *DeviceRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Device, error) {
	q := `SELECT ` + deviceColumns + ` FROM devices d WHERE d.deleted_at IS NULL AND d.id = $1`
	return scanDevice(r.pool.QueryRow(ctx, q, id))
}

type DeviceFilter struct {
	Search             string
	StatutAutorisation string
	StatutMateriel     string
	Flux               string
	Limit              int
	Offset             int
}

func (r *DeviceRepo) List(ctx context.Context, f DeviceFilter) ([]*domain.Device, int, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}

	where := []string{"d.deleted_at IS NULL"}
	args := []any{}
	add := func(cond string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		add(`(d.numero_serie ILIKE '%%' || $%d || '%%'
		      OR d.id_materiel ILIKE '%%' || $%[1]d || '%%'
		      OR d.nom_prenom ILIKE '%%' || $%[1]d || '%%'
		      OR d.marque_modele ILIKE '%%' || $%[1]d || '%%')`, s)
	}
	if f.StatutAutorisation != "" {
		add(`d.statut_autorisation = $%d::statut_autorisation`, f.StatutAutorisation)
	}
	if f.StatutMateriel != "" {
		add(`d.statut_materiel = $%d::statut_materiel`, f.StatutMateriel)
	}
	if f.Flux != "" {
		add(`d.dernier_statut_flux = $%d::sens_flux`, f.Flux)
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM devices d WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	q := `SELECT ` + deviceColumns + ` FROM devices d WHERE ` + clause +
		fmt.Sprintf(` ORDER BY d.id_materiel LIMIT %d OFFSET %d`, f.Limit, f.Offset)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []*domain.Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, d)
	}
	return out, total, rows.Err()
}

// AllSerials sert à la génération de planches de QR codes.
func (r *DeviceRepo) All(ctx context.Context) ([]*domain.Device, error) {
	devices, _, err := r.List(ctx, DeviceFilter{Limit: 500})
	return devices, err
}

// ---------------------------------------------------------------------
// Écritures
// ---------------------------------------------------------------------

// Upsert crée ou met à jour une fiche à partir du numéro de série.
// Renvoie true si la fiche a été créée (false si mise à jour).
func (r *DeviceRepo) Upsert(ctx context.Context, d *domain.Device) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var id uuid.UUID
	var created bool
	err = tx.QueryRow(ctx, `
		INSERT INTO devices (
			id_materiel, numero_serie, type_materiel, marque_modele, matricule,
			nom_prenom, departement, statut_autorisation, date_expiration_autorisation,
			statut_materiel, site_origine, heure_autorisee_debut, heure_autorisee_fin,
			autorise_weekend, commentaire
		) VALUES (
			$1, $2, $3, $4, NULLIF($5,''), $6, $7, $8::statut_autorisation, $9,
			$10::statut_materiel, $11, $12::time, $13::time, $14, $15
		)
		ON CONFLICT (numero_serie) WHERE deleted_at IS NULL DO UPDATE SET
			id_materiel                  = EXCLUDED.id_materiel,
			type_materiel                = EXCLUDED.type_materiel,
			marque_modele                = EXCLUDED.marque_modele,
			matricule                    = EXCLUDED.matricule,
			nom_prenom                   = EXCLUDED.nom_prenom,
			departement                  = EXCLUDED.departement,
			statut_autorisation          = EXCLUDED.statut_autorisation,
			date_expiration_autorisation = EXCLUDED.date_expiration_autorisation,
			statut_materiel              = EXCLUDED.statut_materiel,
			site_origine                 = EXCLUDED.site_origine,
			heure_autorisee_debut        = EXCLUDED.heure_autorisee_debut,
			heure_autorisee_fin          = EXCLUDED.heure_autorisee_fin,
			autorise_weekend             = EXCLUDED.autorise_weekend,
			commentaire                  = EXCLUDED.commentaire,
			updated_at                   = now()
		RETURNING id, (xmax = 0)`,
		d.IDMateriel, d.NumeroSerie, d.TypeMateriel, d.MarqueModele, d.Matricule,
		d.NomPrenom, d.Departement, d.StatutAutorisation, d.DateExpirationAutorisation,
		d.StatutMateriel, d.SiteOrigine, d.HeureAutoriseeDebut, d.HeureAutoriseeFin,
		d.AutoriseWeekend, d.Commentaire,
	).Scan(&id, &created)
	if err != nil {
		return false, translateErr(err)
	}

	if err := replaceZones(ctx, tx, id, d.Zones); err != nil {
		return false, err
	}

	d.ID = id
	return created, tx.Commit(ctx)
}

// Update modifie une fiche existante (interface admin).
func (r *DeviceRepo) Update(ctx context.Context, d *domain.Device) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE devices SET
			id_materiel                  = $2,
			numero_serie                 = $3,
			type_materiel                = $4,
			marque_modele                = $5,
			matricule                    = NULLIF($6,''),
			nom_prenom                   = $7,
			departement                  = $8,
			statut_autorisation          = $9::statut_autorisation,
			date_expiration_autorisation = $10,
			statut_materiel              = $11::statut_materiel,
			site_origine                 = $12,
			heure_autorisee_debut        = $13::time,
			heure_autorisee_fin          = $14::time,
			autorise_weekend             = $15,
			commentaire                  = $16,
			updated_at                   = now()
		WHERE id = $1 AND deleted_at IS NULL`,
		d.ID, d.IDMateriel, d.NumeroSerie, d.TypeMateriel, d.MarqueModele, d.Matricule,
		d.NomPrenom, d.Departement, d.StatutAutorisation, d.DateExpirationAutorisation,
		d.StatutMateriel, d.SiteOrigine, d.HeureAutoriseeDebut, d.HeureAutoriseeFin,
		d.AutoriseWeekend, d.Commentaire)
	if err != nil {
		return translateErr(err)
	}
	if ct.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	if err := replaceZones(ctx, tx, d.ID, d.Zones); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SoftDelete archive la fiche sans jamais supprimer l'historique associé.
func (r *DeviceRepo) SoftDelete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx,
		`UPDATE devices SET deleted_at = now(), updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func replaceZones(ctx context.Context, tx pgx.Tx, deviceID uuid.UUID, zones []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM device_zones WHERE device_id = $1`, deviceID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, z := range zones {
		z = strings.TrimSpace(z)
		if z == "" || seen[strings.ToLower(z)] {
			continue
		}
		seen[strings.ToLower(z)] = true
		if _, err := tx.Exec(ctx,
			`INSERT INTO device_zones (device_id, zone) VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`, deviceID, z); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// Scan : écriture du log + mise à jour du cache de flux, en une transaction
// ---------------------------------------------------------------------

// RecordScan insère la ligne d'audit (immuable) et met à jour les colonnes
// dénormalisées de la fiche. Le statut de flux n'est modifié que si le
// mouvement a réellement eu lieu (accès accordé) ; l'horodatage du dernier
// scan, lui, est toujours mis à jour car le passage à la guérite a bien eu lieu.
func (r *DeviceRepo) RecordScan(ctx context.Context, l *domain.AuditLog, majFlux bool) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO audit_logs (
			device_id, numero_serie, id_materiel, nom_prenom, scanned_at,
			journee_logique, sens_suggere, sens_confirme, decision, access_granted,
			motifs, derogation, justification, point_controle, zone,
			agent_id, agent_nom, ip_address, user_agent, snapshot
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7::sens_flux, $8::sens_flux, $9::decision_scan, $10,
			$11, $12, $13, $14, $15, $16, $17, $18, $19, $20::jsonb
		) RETURNING id`,
		l.DeviceID, l.NumeroSerie, l.IDMateriel, l.NomPrenom, l.ScannedAt,
		l.JourneeLogique, l.SensSuggere, l.SensConfirme, l.Decision, l.AccessGranted,
		l.Motifs, l.Derogation, l.Justification, l.PointControle, l.Zone,
		l.AgentID, l.AgentNom, l.IPAddress, l.UserAgent, string(l.Snapshot),
	).Scan(&id)
	if err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE devices SET
			horodatage_dernier_scan = $2,
			dernier_statut_flux = CASE WHEN $3 THEN $4::sens_flux ELSE dernier_statut_flux END,
			updated_at = now()
		WHERE id = $1`, l.DeviceID, l.ScannedAt, majFlux, l.SensConfirme); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	l.ID = id
	return id, nil
}

// ---------------------------------------------------------------------

func translateErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "duplicate key") || strings.Contains(msg, "23505") {
		return fmt.Errorf("%w: numéro de série ou ID matériel déjà utilisé", domain.ErrDuplicate)
	}
	return err
}

// Stats renvoie quelques compteurs pour le tableau de bord admin.
type Stats struct {
	Total        int `json:"total"`
	Autorises    int `json:"autorises"`
	Sortis       int `json:"sortis"`
	Alertes      int `json:"alertes"`
	ScansDuJour  int `json:"scans_du_jour"`
	RefusDuJour  int `json:"refus_du_jour"`
	DerogsDuJour int `json:"derogations_du_jour"`
}

func (r *DeviceRepo) Stats(ctx context.Context, journee time.Time) (*Stats, error) {
	var s Stats
	err := r.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM devices WHERE deleted_at IS NULL),
			(SELECT count(*) FROM devices WHERE deleted_at IS NULL AND statut_autorisation = 'AUTORISE'),
			(SELECT count(*) FROM devices WHERE deleted_at IS NULL AND dernier_statut_flux = 'SORTI'),
			(SELECT count(*) FROM devices WHERE deleted_at IS NULL AND statut_materiel IN ('VOLE_PERDU','EN_MAINTENANCE')),
			(SELECT count(*) FROM audit_logs WHERE journee_logique = $1::date),
			(SELECT count(*) FROM audit_logs WHERE journee_logique = $1::date AND access_granted = false),
			(SELECT count(*) FROM audit_logs WHERE journee_logique = $1::date AND derogation = true)`,
		journee).Scan(&s.Total, &s.Autorises, &s.Sortis, &s.Alertes,
		&s.ScansDuJour, &s.RefusDuJour, &s.DerogsDuJour)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
