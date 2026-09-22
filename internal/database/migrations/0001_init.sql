-- =====================================================================
-- Hardware PassGuard - Schéma initial
-- PostgreSQL >= 14 (gen_random_uuid() natif)
-- =====================================================================

-- ---------- Types énumérés (créés de façon idempotente) --------------
DO $$ BEGIN
    CREATE TYPE statut_autorisation AS ENUM ('AUTORISE','NON_AUTORISE','SUSPENDU','TEMPORAIRE');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE statut_materiel AS ENUM ('ACTIF','EN_MAINTENANCE','VOLE_PERDU','REFORME');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE sens_flux AS ENUM ('ENTRE','SORTI','INCONNU');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE decision_scan AS ENUM ('AUTORISE','REFUSE','ALERTE');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE role_utilisateur AS ENUM ('ADMIN','VIGILE');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ---------- Comptes (admins + vigiles) -------------------------------
CREATE TABLE IF NOT EXISTS admins (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email              TEXT UNIQUE NOT NULL,
    nom_prenom         TEXT NOT NULL,
    mot_de_passe       TEXT NOT NULL,
    role               role_utilisateur NOT NULL DEFAULT 'VIGILE',
    actif              BOOLEAN NOT NULL DEFAULT TRUE,
    derniere_connexion TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------- Devices --------------------------------------------------
CREATE TABLE IF NOT EXISTS devices (
    id                           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    id_materiel                  TEXT NOT NULL,
    numero_serie                 TEXT NOT NULL,
    type_materiel                TEXT NOT NULL,
    marque_modele                TEXT NOT NULL DEFAULT '',
    matricule                    TEXT,
    nom_prenom                   TEXT NOT NULL,
    departement                  TEXT NOT NULL DEFAULT '',
    statut_autorisation          statut_autorisation NOT NULL DEFAULT 'NON_AUTORISE',
    date_expiration_autorisation DATE,
    statut_materiel              statut_materiel NOT NULL DEFAULT 'ACTIF',
    site_origine                 TEXT NOT NULL DEFAULT '',
    heure_autorisee_debut        TIME NOT NULL DEFAULT '07:00',
    heure_autorisee_fin          TIME NOT NULL DEFAULT '18:00',
    autorise_weekend             BOOLEAN NOT NULL DEFAULT FALSE,
    dernier_statut_flux          sens_flux NOT NULL DEFAULT 'INCONNU',
    horodatage_dernier_scan      TIMESTAMPTZ,
    commentaire                  TEXT NOT NULL DEFAULT '',
    created_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at                   TIMESTAMPTZ
);

-- Unicité métier : on n'autorise pas deux fiches vivantes avec le même S/N.
CREATE UNIQUE INDEX IF NOT EXISTS devices_numero_serie_uniq
    ON devices (numero_serie) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS devices_id_materiel_uniq
    ON devices (id_materiel) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS devices_nom_prenom_idx ON devices (lower(nom_prenom));
CREATE INDEX IF NOT EXISTS devices_statut_idx ON devices (statut_autorisation, statut_materiel);

-- ---------- Zones autorisées (normalisation du multi-valeur) ---------
CREATE TABLE IF NOT EXISTS device_zones (
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    zone      TEXT NOT NULL,
    PRIMARY KEY (device_id, zone)
);

-- ---------- Journal d'audit (immuable) -------------------------------
CREATE TABLE IF NOT EXISTS audit_logs (
    id              BIGSERIAL PRIMARY KEY,
    device_id       UUID NOT NULL REFERENCES devices(id),
    numero_serie    TEXT NOT NULL,
    id_materiel     TEXT NOT NULL,
    nom_prenom      TEXT NOT NULL,
    scanned_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    journee_logique DATE NOT NULL,
    sens_suggere    sens_flux NOT NULL,
    sens_confirme   sens_flux NOT NULL,
    decision        decision_scan NOT NULL,
    access_granted  BOOLEAN NOT NULL,
    motifs          TEXT[] NOT NULL DEFAULT '{}',
    derogation      BOOLEAN NOT NULL DEFAULT FALSE,
    justification   TEXT NOT NULL DEFAULT '',
    point_controle  TEXT NOT NULL DEFAULT '',
    zone            TEXT NOT NULL DEFAULT '',
    agent_id        UUID REFERENCES admins(id),
    agent_nom       TEXT NOT NULL DEFAULT '',
    ip_address      TEXT NOT NULL DEFAULT '',
    user_agent      TEXT NOT NULL DEFAULT '',
    snapshot        JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS audit_logs_device_idx ON audit_logs (device_id, scanned_at DESC);
CREATE INDEX IF NOT EXISTS audit_logs_scanned_idx ON audit_logs (scanned_at DESC);
CREATE INDEX IF NOT EXISTS audit_logs_journee_idx ON audit_logs (journee_logique);
CREATE INDEX IF NOT EXISTS audit_logs_decision_idx ON audit_logs (decision);

-- Immuabilité garantie par la base : aucun UPDATE / DELETE possible.
-- Pour corriger une erreur, on insère une ligne de rectification.
DROP RULE IF EXISTS audit_logs_no_update ON audit_logs;
CREATE RULE audit_logs_no_update AS ON UPDATE TO audit_logs DO INSTEAD NOTHING;
DROP RULE IF EXISTS audit_logs_no_delete ON audit_logs;
CREATE RULE audit_logs_no_delete AS ON DELETE TO audit_logs DO INSTEAD NOTHING;

-- ---------- Lots d'import Excel --------------------------------------
CREATE TABLE IF NOT EXISTS import_batches (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id      UUID REFERENCES admins(id),
    nom_fichier   TEXT NOT NULL,
    lignes_total  INT NOT NULL DEFAULT 0,
    lignes_creees INT NOT NULL DEFAULT 0,
    lignes_majs   INT NOT NULL DEFAULT 0,
    lignes_erreur INT NOT NULL DEFAULT 0,
    rapport       JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS import_batches_created_idx ON import_batches (created_at DESC);
