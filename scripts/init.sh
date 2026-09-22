#!/usr/bin/env bash
# =====================================================================
# Hardware PassGuard - initialisation de l'environnement de développement
#
#   ./scripts/init.sh
#
# Le script est idempotent : on peut le relancer sans risque.
# =====================================================================
set -euo pipefail

RACINE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$RACINE"

vert()  { printf '\033[0;32m%s\033[0m\n' "$1"; }
jaune() { printf '\033[0;33m%s\033[0m\n' "$1"; }
rouge() { printf '\033[0;31m%s\033[0m\n' "$1"; }

echo "==============================================="
echo " Hardware PassGuard - initialisation"
echo "==============================================="

# --- 1. Prérequis ----------------------------------------------------
manquant=0
verifier() {
  if command -v "$1" >/dev/null 2>&1; then
    vert "  [ok]      $1 ($($2 2>/dev/null | head -1))"
  else
    rouge "  [absent]  $1 - $3"
    manquant=1
  fi
}

echo ""
echo "1. Vérification des prérequis"
verifier go "go version" "installer Go 1.22+ : https://go.dev/dl/"
verifier psql "psql --version" "installer le client PostgreSQL (paquet postgresql-client)"

if [ "$manquant" -eq 1 ]; then
  rouge ""
  rouge "Des prérequis sont manquants. Installez-les puis relancez ce script."
  rouge "Alternative sans installation locale : docker compose up -d"
  exit 1
fi

# --- 2. Fichier .env -------------------------------------------------
echo ""
echo "2. Configuration"
if [ -f .env ]; then
  jaune "  .env existe déjà, il n'est pas écrasé."
else
  cp .env.example .env
  if command -v openssl >/dev/null 2>&1; then
    SECRET="$(openssl rand -base64 48 | tr -d '\n/+=' | cut -c1-48)"
    # Compatible GNU sed et BSD sed
    sed -i.bak "s|^JWT_SECRET=.*|JWT_SECRET=${SECRET}|" .env && rm -f .env.bak
    vert "  .env créé avec un JWT_SECRET aléatoire."
  else
    jaune "  .env créé. Pensez à remplacer JWT_SECRET manuellement."
  fi
  jaune "  Vérifiez DATABASE_URL, ADMIN_EMAIL et ADMIN_PASSWORD dans .env."
fi

# --- 3. Base de données ----------------------------------------------
echo ""
echo "3. Base de données"
DB_URL="$(grep -E '^DATABASE_URL=' .env | cut -d= -f2- || true)"
if psql "$DB_URL" -c 'SELECT 1' >/dev/null 2>&1; then
  vert "  Connexion PostgreSQL réussie."
  VERSION="$(psql "$DB_URL" -tAc 'SHOW server_version_num' || echo 0)"
  if [ "${VERSION:-0}" -lt 140000 ]; then
    jaune "  PostgreSQL 14+ recommandé (gen_random_uuid natif)."
  fi
else
  jaune "  Connexion impossible avec DATABASE_URL."
  jaune "  Créez la base, par exemple :"
  echo ""
  echo "    sudo -u postgres psql -c \"CREATE USER passguard WITH PASSWORD 'passguard';\""
  echo "    sudo -u postgres psql -c \"CREATE DATABASE passguard OWNER passguard;\""
  echo ""
  jaune "  Les tables, elles, sont créées automatiquement au démarrage du serveur."
fi

# --- 4. Dépendances Go -----------------------------------------------
echo ""
echo "4. Dépendances Go"
go mod tidy
vert "  go.sum généré / mis à jour."

# --- 5. Compilation et tests -----------------------------------------
echo ""
echo "5. Compilation"
mkdir -p bin
go build -o bin/passguard ./cmd/server
vert "  binaire : bin/passguard"

echo ""
echo "6. Tests unitaires du moteur de décision"
go test ./internal/service/ || jaune "  Des tests ont échoué, vérifiez la sortie ci-dessus."

echo ""
echo "==============================================="
vert " Initialisation terminée."
echo "==============================================="
echo ""
echo "  Démarrer        : ./bin/passguard     (ou: go run ./cmd/server)"
echo "  Poste de contrôle : http://localhost:8080/scan"
echo "  Administration    : http://localhost:8080/admin"
echo "  Fichier d'exemple : samples/devices_exemple.xlsx"
echo ""
