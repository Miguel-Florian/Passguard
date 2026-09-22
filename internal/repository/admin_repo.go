package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/entreprise/device-auth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AdminRepo struct {
	pool *pgxpool.Pool
}

func NewAdminRepo(pool *pgxpool.Pool) *AdminRepo { return &AdminRepo{pool: pool} }

const adminColumns = `id, email, nom_prenom, mot_de_passe, role::text, actif, derniere_connexion, created_at`

func scanAdmin(row pgx.Row) (*domain.Admin, error) {
	var a domain.Admin
	err := row.Scan(&a.ID, &a.Email, &a.NomPrenom, &a.MotDePasse, &a.Role,
		&a.Actif, &a.DerniereConnexion, &a.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

func (r *AdminRepo) GetByEmail(ctx context.Context, email string) (*domain.Admin, error) {
	return scanAdmin(r.pool.QueryRow(ctx,
		`SELECT `+adminColumns+` FROM admins WHERE email = $1`,
		strings.ToLower(strings.TrimSpace(email))))
}

func (r *AdminRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Admin, error) {
	return scanAdmin(r.pool.QueryRow(ctx, `SELECT `+adminColumns+` FROM admins WHERE id = $1`, id))
}

func (r *AdminRepo) List(ctx context.Context) ([]*domain.Admin, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+adminColumns+` FROM admins ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*domain.Admin{}
	for rows.Next() {
		a, err := scanAdmin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *AdminRepo) Create(ctx context.Context, a *domain.Admin) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO admins (email, nom_prenom, mot_de_passe, role, actif)
		VALUES ($1, $2, $3, $4::role_utilisateur, $5)
		RETURNING id, created_at`,
		strings.ToLower(strings.TrimSpace(a.Email)), a.NomPrenom, a.MotDePasse, a.Role, a.Actif,
	).Scan(&a.ID, &a.CreatedAt)
	return translateErr(err)
}

// EnsureExists crée le compte s'il n'existe pas déjà (amorçage au démarrage).
// Renvoie true si le compte vient d'être créé.
func (r *AdminRepo) EnsureExists(ctx context.Context, a *domain.Admin) (bool, error) {
	existing, err := r.GetByEmail(ctx, a.Email)
	if err == nil {
		a.ID = existing.ID
		return false, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return false, err
	}
	if err := r.Create(ctx, a); err != nil {
		return false, err
	}
	return true, nil
}

func (r *AdminRepo) TouchLogin(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE admins SET derniere_connexion = now(), updated_at = now() WHERE id = $1`, id)
	return err
}

func (r *AdminRepo) UpdatePassword(ctx context.Context, id uuid.UUID, hash string) error {
	ct, err := r.pool.Exec(ctx,
		`UPDATE admins SET mot_de_passe = $2, updated_at = now() WHERE id = $1`, id, hash)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *AdminRepo) SetActif(ctx context.Context, id uuid.UUID, actif bool) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE admins SET actif = $2, updated_at = now() WHERE id = $1`, id, actif)
	return err
}
