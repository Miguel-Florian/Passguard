# Hardware PassGuard

Serveur d'authentification de matériel par QR code, écrit en Go (Gin + PostgreSQL).

Il répond à une question simple posée à la guérite : **est-ce que cet appareil a
le droit de sortir du bâtiment, maintenant, avec cette personne ?** Et il garde
une trace inaltérable de chaque passage.

---

## 1. Fonctionnement en trois temps

### Création
L'administrateur ajoute un matériel (formulaire unitaire ou import Excel en masse).
Le serveur crée la fiche puis génère un QR code contenant l'URL de vérification :

```
https://passguard.monentreprise.cm/v/SN-HP-889021
```

L'étiquette est imprimée et collée sur l'appareil. N'importe quel appareil photo
de téléphone l'ouvre : aucune application dédiée n'est nécessaire.

Le QR code affiché à l'écran (fiche matériel, planche d'étiquettes) est généré
à la volée et n'est jamais écrit sur disque automatiquement. Si vous voulez une
copie fichier de tous les QR codes — pour un autre outil, une sauvegarde, ou une
imprimante d'étiquettes qui lit un dossier — cliquez sur **« Régénérer les QR
codes »** dans la console d'administration : le serveur écrit un fichier
`NUMERO_SERIE.png` par matériel dans le dossier `QR_CODES_DIR` (`./qr_codes`
par défaut). C'est un dossier **interne au serveur, jamais servi au
navigateur** ; relancez cette action après tout changement de `BASE_URL`,
puisque l'URL encodée dans chaque image en dépend.

### Vérification (lecture seule)
Le vigile scanne l'étiquette, la page s'ouvre et affiche la fiche du matériel,
son détenteur, ses zones, et le verdict du moteur de règles.
**Cette consultation n'écrit rien** : regarder une fiche n'est pas un mouvement.

### Enregistrement (écriture)
Le vigile connecté valide le sens réel du mouvement avec les boutons
**ENTRÉE** / **SORTIE**. Le serveur pré-sélectionne le sens qu'il a déduit, le
vigile confirme ou corrige. C'est cette action, et elle seule, qui écrit une
ligne dans le journal d'audit.

---

## 2. La logique de `dernier_statut_flux`

C'était la question centrale du projet. La réponse tient en trois idées.

### a) Le journal est la vérité, la colonne est un cache
`audit_logs` est la source unique. `devices.dernier_statut_flux` et
`devices.horodatage_dernier_scan` sont des colonnes dénormalisées, mises à jour
dans la **même transaction** que l'insertion du log, et reconstructibles à tout
moment. La table `audit_logs` est protégée par deux règles PostgreSQL :

```sql
CREATE RULE audit_logs_no_update AS ON UPDATE TO audit_logs DO INSTEAD NOTHING;
CREATE RULE audit_logs_no_delete AS ON DELETE TO audit_logs DO INSTEAD NOTHING;
```

Même un `UPDATE` lancé à la main en console ne modifie rien. Une correction se
fait en insérant une ligne de rectification, jamais en écrasant l'historique.
Chaque ligne embarque aussi un `snapshot` JSONB de l'état du device au moment du
scan : si l'administrateur modifie la fiche demain, le log d'hier reste opposable.

### b) La journée logique (pivot à 05:00)
Les horaires débordent le soir (jusqu'à 21h avec dérogation). Une journée de
travail ne va donc pas de minuit à minuit. Avec `PIVOT_HOUR=5`, tout scan entre
05:00 et 04:59 le lendemain appartient à la même journée logique. Un départ à
20h50 et un retour à 07h le lendemain sont correctement interprétés.

### c) L'alternance, corrigée par le vigile
Le serveur **suggère** le sens :

| Dernier état connu | Journée du dernier scan | Sens suggéré | Remarque |
|---|---|---|---|
| jamais scanné | — | `ENTRE` | premier passage |
| `SORTI` | quelconque | `ENTRE` | le matériel revient |
| `ENTRE` | journée en cours | `SORTI` | alternance normale |
| `ENTRE` | journée antérieure | `ENTRE` | **anomalie** : sorti sans scan |

Le vigile confirme ou corrige. Les deux valeurs sont stockées (`sens_suggere` et
`sens_confirme`) : leur écart est précisément ce qui permet de détecter, plus
tard, les scans manqués et les erreurs de poste. Le statut de flux ne bascule
que si l'accès est accordé — un refus n'est pas un mouvement — tandis que
l'horodatage du dernier scan, lui, est toujours mis à jour puisque le passage à
la guérite a bien eu lieu.

### d) Les règles de sortie
Évaluées dans cet ordre, chacune produisant un code de motif stable :

1. `statut_materiel` ∈ {`VOLE_PERDU`, `EN_MAINTENANCE`, `REFORME`} — `VOLE_PERDU` déclenche une **ALERTE**
2. `statut_autorisation` = `NON_AUTORISE` ou `SUSPENDU`
3. `date_expiration_autorisation` dépassée
4. heure courante hors de `[heure_autorisee_debut, heure_autorisee_fin]`
5. week-end alors que `autorise_weekend` = false
6. zone du point de contrôle non couverte (`Zone-Toutes` est un joker, `Aucune` refuse toujours)

Une **entrée** n'est jamais bloquée pour un motif d'habilitation : un matériel
qui rentre ne présente pas de risque, on veut au contraire l'enregistrer. Seul un
matériel déclaré volé déclenche une alerte à l'entrée (il faut le retenir).

### e) La dérogation 18h → 21h
Quand le seul motif bloquant est le dépassement horaire *en fin de journée*, et
que l'heure limite (`DEROGATION_MAX_HOUR`) n'est pas franchie, le vigile voit
apparaître un bouton « Autoriser avec justification ». La sortie est alors
accordée, marquée `derogation = true`, avec le texte saisi et le nom de l'agent.

Vous obtenez un rapport nominatif des sorties tardives au lieu d'un blocage sec
que les gens contourneraient en passant par une autre porte. La dérogation ne
couvre **que** l'horaire : elle est indisponible dès qu'un autre motif bloque.

---

## 3. Prérequis

| Outil | Version | Pourquoi |
|---|---|---|
| **Go** | 1.22 ou supérieur | compilation du serveur |
| **PostgreSQL** | 14 ou supérieur | `gen_random_uuid()` natif, types ENUM, `RULE` |
| **Git** | quelconque | récupération du code |
| **openssl** | optionnel | génération du `JWT_SECRET` par `init.sh` |
| **Docker + Docker Compose** | optionnel | alternative : tout tourne sans rien installer |

Installation des prérequis sur Ubuntu / Debian :

```bash
sudo apt update
sudo apt install -y golang-go postgresql postgresql-client git openssl
# Si le paquet golang-go est trop ancien (go version < 1.22) :
#   https://go.dev/dl/  puis  export PATH=$PATH:/usr/local/go/bin
```

Aucune dépendance système supplémentaire n'est nécessaire : les templates HTML,
les fichiers statiques et les migrations SQL sont **embarqués dans le binaire**
via `go:embed`, et la base de fuseaux horaires via `time/tzdata`. Le binaire
compilé est autonome.

Dépendances Go (téléchargées par `go mod tidy`) :

```
gin-gonic/gin          serveur HTTP
jackc/pgx/v5           pilote PostgreSQL
xuri/excelize/v2       lecture et écriture des fichiers .xlsx
skip2/go-qrcode        génération des QR codes
golang-jwt/jwt/v5      jetons d'authentification
golang.org/x/crypto    hachage bcrypt des mots de passe
google/uuid            identifiants
joho/godotenv          chargement du fichier .env
```

---

## 4. Installation

### Option A — script d'initialisation (recommandé)

```bash
git clone <votre-depot> passguard && cd passguard
./scripts/init.sh
```

Le script vérifie les prérequis, crée le `.env` avec un `JWT_SECRET` aléatoire,
teste la connexion PostgreSQL, télécharge les dépendances, compile et lance les
tests. Il est idempotent : relançable sans risque.

Créez la base si elle n'existe pas :

```bash
sudo -u postgres psql -c "CREATE USER passguard WITH PASSWORD 'passguard';"
sudo -u postgres psql -c "CREATE DATABASE passguard OWNER passguard;"
```

Puis démarrez :

```bash
./bin/passguard          # ou : make run
```

**Les tables sont créées automatiquement au premier démarrage** (migrations
embarquées, suivies dans `schema_migrations`), tout comme le compte
administrateur défini dans le `.env`.

### Option B — Docker

```bash
cp .env.example .env     # ajustez au besoin
docker compose up -d --build
```

PostgreSQL et l'application démarrent ensemble ; la base est persistée dans le
volume `pgdata`.

### Option C — manuelle

```bash
cp .env.example .env && $EDITOR .env
go mod tidy
go build -o bin/passguard ./cmd/server
./bin/passguard
```

---

## 5. Premiers pas

| URL | Qui | Quoi |
|---|---|---|
| `/login` | tous | connexion |
| `/admin` | ADMIN | console : matériels, import, journal d'audit |
| `/scan` | VIGILE, ADMIN | poste de contrôle |
| `/v/:numero_serie` | selon `PUBLIC_VERIFICATION` | cible des QR codes |
| `/admin/etiquettes` | ADMIN | planche de QR codes à imprimer (Ctrl+P → PDF) |

1. Connectez-vous avec `ADMIN_EMAIL` / `ADMIN_PASSWORD`.
2. Onglet **Import Excel** → importez `samples/devices_exemple.xlsx` (il reprend
   vos dix matériels réels).
3. Onglet **Matériels** → bouton QR code sur une ligne, ou **Planche
   d'étiquettes** pour tout imprimer d'un coup.
4. Ouvrez `/v/SN-HP-889021` (ou scannez l'étiquette) pour voir la page guérite.
5. Connecté en vigile, validez un mouvement : le journal d'audit se remplit.

### Format du fichier d'import

Colonnes reconnues (la comparaison ignore casse, accents, espaces et underscores,
donc `Numéro de série` équivaut à `Numero_Serie`) :

```
ID_Materiel, Numero_Serie*, Type_Materiel, Marque_Modele, Matricule,
Nom_Prenom*, Departement, Statut_Autorisation, Date_Expiration_Autorisation,
Statut_Materiel, Site_Origine, Zone_Autorisee, Heure_Autorisee_Debut,
Heure_Autorisee_Fin, Autorise_Weekend, Commentaire
```

`*` = obligatoire. Les fiches existantes sont mises à jour d'après le numéro de
série. Un rapport ligne par ligne est renvoyé et archivé dans `import_batches`.

Deux points d'attention hérités de votre fichier actuel :

- **`Dernier_Statut_Flux` et `Horodatage_Dernier_Scan` sont ignorés à l'import.**
  Ces valeurs se déduisent des scans ; les importer reviendrait à fabriquer un
  historique qui n'a jamais eu lieu. Chaque fiche démarre en `INCONNU` et se
  resynchronise au premier passage à la guérite.
- **`Statut_Materiel = TEMPORAIRE`** (cas de MAT-007) mélange deux notions. À
  l'import, la valeur est reclassée en `statut_materiel = ACTIF` +
  `statut_autorisation = TEMPORAIRE`, avec un avertissement dans le rapport.

---

## 6. API

Authentification par cookie `HttpOnly` (navigateur) ou en-tête
`Authorization: Bearer <jeton>` (intégrations).

### Public / vigile
```
POST   /api/auth/login              { email, password }
POST   /api/auth/logout
GET    /api/auth/me
GET    /api/verify/:sn              consultation, n'écrit rien
POST   /api/scan/:sn                { sens, zone, derogation, justification }   VIGILE|ADMIN
GET    /healthz
```

### Administration (rôle ADMIN)
```
GET    /api/stats
GET    /api/devices?q=&statut_autorisation=&limit=&offset=
POST   /api/devices
GET    /api/devices/:id
PATCH  /api/devices/:id
DELETE /api/devices/:id             archivage logique, l'historique est conservé
GET    /api/devices/:id/qrcode.png?taille=520
POST   /api/qrcodes/regenerate      écrit un PNG par matériel dans QR_CODES_DIR (disque serveur)
POST   /api/import/devices          multipart, champ "file"
GET    /api/import/template.xlsx
GET    /api/import/history
GET    /api/export/devices.xlsx
GET    /api/export/audit.xlsx?serie=&decision=&du=&au=&derogation=
GET    /api/audit?serie=&decision=&du=&au=&derogation=&limit=&offset=
```

Exemple d'enregistrement d'une sortie avec dérogation :

```bash
curl -X POST http://localhost:8080/api/scan/SN-HP-889021 \
  -H "Authorization: Bearer $JETON" \
  -H "Content-Type: application/json" \
  -d '{"sens":"SORTI","derogation":true,"justification":"Astreinte projet Alpha"}'
```

---

## 7. Structure du projet

```
cmd/server/main.go              démarrage, migrations, amorçage, arrêt gracieux
internal/config/                chargement et validation du .env
internal/domain/                entités et constantes métier, sans dépendance
internal/database/              pool pgx + migrations embarquées
  migrations/0001_init.sql      schéma complet
internal/repository/            accès PostgreSQL (devices, admins, audit)
internal/service/
  flux.go                       MOTEUR DE DÉCISION (fonctions pures)
  flux_test.go                  tests unitaires du moteur
  scan.go                       orchestration transactionnelle du scan
  importer.go                   parsing Excel tolérant + rapport
  export.go                     exports .xlsx
  qrcode.go                     QR codes et étiquettes
  auth.go                       bcrypt + JWT + amorçage des comptes
internal/handler/               routes Gin, API JSON, pages HTML
  templates/                    verification, login, admin, etiquettes
internal/middleware/            authentification, rôles, limitation de débit
samples/devices_exemple.xlsx    jeu de données de test
scripts/init.sh                 initialisation de l'environnement
```

Le moteur de décision (`flux.go`) ne dépend ni de la base ni de HTTP : il est
testable en isolation. `go test ./internal/service/` couvre l'alternance, la
journée logique, les dérogations, l'alerte vol et les zones.

---

## 8. Mise en production

```bash
# 1. Variables
APP_ENV=production
PUBLIC_VERIFICATION=false            # la page publique n'affiche plus de nom
JWT_SECRET=$(openssl rand -base64 48)
ADMIN_PASSWORD=<mot de passe fort>
BASE_URL=https://passguard.monentreprise.cm   # DOIT être définitive : elle est
                                              # gravée dans les QR codes imprimés

# 2. Compilation
CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/passguard ./cmd/server
```

Checklist :

- [ ] **Fixer `BASE_URL` avant d'imprimer les étiquettes.** Changer de domaine
      ensuite oblige à tout réimprimer.
- [ ] HTTPS obligatoire (les cookies passent en `Secure` dès `APP_ENV=production`).
- [ ] `PUBLIC_VERIFICATION=false` : sans connexion, un visiteur ne voit qu'un
      statut minimal, sans nom ni département.
- [ ] Changer les mots de passe par défaut ; créer un compte vigile par poste
      plutôt qu'un compte partagé (l'audit est nominatif).
- [ ] Sauvegarder `audit_logs` (`pg_dump`) : c'est la pièce justificative.
- [ ] Vérifier le fuseau (`TZ=Africa/Douala`) — toute la logique horaire en dépend.
- [ ] Si vous utilisez le dossier `QR_CODES_DIR`, montez-le sur un volume
      persistant (voir `docker-compose.yml`) : sinon son contenu disparaît au
      redémarrage du conteneur. Il ne contient qu'une copie régénérable, pas
      une donnée source, donc ce n'est jamais bloquant.

### Service systemd

```ini
[Unit]
Description=Hardware PassGuard
After=network.target postgresql.service

[Service]
Type=simple
User=passguard
WorkingDirectory=/opt/passguard
EnvironmentFile=/opt/passguard/.env
ExecStart=/opt/passguard/bin/passguard
Restart=always
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

### Reverse proxy nginx

```nginx
server {
    listen 443 ssl http2;
    server_name passguard.monentreprise.cm;

    ssl_certificate     /etc/letsencrypt/live/passguard/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/passguard/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Avec un proxy, pensez à `router.SetTrustedProxies([]string{"127.0.0.1"})` si vous
souhaitez que `c.ClientIP()` reflète l'IP réelle dans le journal d'audit.

---

## 9. Limites connues et évolutions possibles

- **Le QR contient le numéro de série en clair**, donc énumérable. C'est le choix
  retenu (lisible, réimprimable, indépendant de la base). Si le risque devient
  gênant, ajoutez un jeton opaque ou une signature HMAC dans l'URL sans changer
  le reste de l'architecture : seule `URLVerification()` est à modifier.
- Un seul point de contrôle est modélisé (`POINT_CONTROLE`). Pour plusieurs
  guérites, passez-le en colonne de la table `admins` ou en paramètre d'URL.
- Pas encore de notification (mail / SMS) sur détection d'un matériel volé :
  le point d'accroche naturel est `Decision == ALERTE` dans `ScanService.Record`.
- La planche d'étiquettes s'imprime depuis le navigateur ; une génération PDF
  côté serveur demanderait une bibliothèque supplémentaire.
