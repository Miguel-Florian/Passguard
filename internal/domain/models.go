package domain

import (
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------
// Énumérations métier (miroir des types PostgreSQL)
// ---------------------------------------------------------------------

// Statut d'autorisation de sortie.
const (
	AutorisationAutorisee    = "AUTORISE"
	AutorisationNonAutorisee = "NON_AUTORISE"
	AutorisationSuspendue    = "SUSPENDU"
	AutorisationTemporaire   = "TEMPORAIRE"
)

// État physique / administratif du matériel.
const (
	MaterielActif         = "ACTIF"
	MaterielEnMaintenance = "EN_MAINTENANCE"
	MaterielVolePerdu     = "VOLE_PERDU"
	MaterielReforme       = "REFORME"
)

// Sens du flux.
const (
	FluxEntre   = "ENTRE"
	FluxSorti   = "SORTI"
	FluxInconnu = "INCONNU"
)

// Décision rendue par le moteur de règles.
const (
	DecisionAutorise = "AUTORISE"
	DecisionRefuse   = "REFUSE"
	DecisionAlerte   = "ALERTE"
)

// Rôles.
const (
	RoleAdmin  = "ADMIN"
	RoleVigile = "VIGILE"
)

// Motifs de refus / d'alerte. Ce sont des codes stables, jamais traduits
// en base : la traduction se fait à l'affichage.
const (
	MotifMaterielVolePerdu     = "MATERIEL_VOLE_PERDU"
	MotifMaterielEnMaintenance = "MATERIEL_EN_MAINTENANCE"
	MotifMaterielReforme       = "MATERIEL_REFORME"
	MotifNonAutorise           = "AUTORISATION_NON_AUTORISEE"
	MotifSuspendu              = "AUTORISATION_SUSPENDUE"
	MotifExpire                = "AUTORISATION_EXPIREE"
	MotifHorsPlageHoraire      = "HORS_PLAGE_HORAIRE"
	MotifWeekendNonAutorise    = "WEEKEND_NON_AUTORISE"
	MotifZoneNonAutorisee      = "ZONE_NON_AUTORISEE"
	MotifAnomalieFlux          = "ANOMALIE_FLUX"
	MotifDerogationAccordee    = "DEROGATION_ACCORDEE"
)

// LibellesMotifs traduit un code motif pour l'affichage.
var LibellesMotifs = map[string]string{
	MotifMaterielVolePerdu:     "Matériel déclaré volé ou perdu",
	MotifMaterielEnMaintenance: "Matériel en maintenance",
	MotifMaterielReforme:       "Matériel réformé",
	MotifNonAutorise:           "Matériel non autorisé à sortir",
	MotifSuspendu:              "Autorisation suspendue",
	MotifExpire:                "Autorisation expirée",
	MotifHorsPlageHoraire:      "Hors de la plage horaire autorisée",
	MotifWeekendNonAutorise:    "Sortie week-end non autorisée",
	MotifZoneNonAutorisee:      "Zone non couverte par l'autorisation",
	MotifAnomalieFlux:          "Incohérence de flux (scan précédent manquant)",
	MotifDerogationAccordee:    "Dérogation accordée par le vigile",
}

func LibelleMotif(code string) string {
	if l, ok := LibellesMotifs[code]; ok {
		return l
	}
	return code
}

// ZoneToutes est le joker couvrant toutes les zones.
const ZoneToutes = "Zone-Toutes"

// ZoneAucune signifie explicitement "aucune zone autorisée".
const ZoneAucune = "Aucune"

// ---------------------------------------------------------------------
// Entités
// ---------------------------------------------------------------------

type Admin struct {
	ID                uuid.UUID  `json:"id"`
	Email             string     `json:"email"`
	NomPrenom         string     `json:"nom_prenom"`
	MotDePasse        string     `json:"-"`
	Role              string     `json:"role"`
	Actif             bool       `json:"actif"`
	DerniereConnexion *time.Time `json:"derniere_connexion,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

func (a *Admin) IsAdmin() bool { return a.Role == RoleAdmin }

type Device struct {
	ID                         uuid.UUID  `json:"id"`
	IDMateriel                 string     `json:"id_materiel"`
	NumeroSerie                string     `json:"numero_serie"`
	TypeMateriel               string     `json:"type_materiel"`
	MarqueModele               string     `json:"marque_modele"`
	Matricule                  string     `json:"matricule"`
	NomPrenom                  string     `json:"nom_prenom"`
	Departement                string     `json:"departement"`
	StatutAutorisation         string     `json:"statut_autorisation"`
	DateExpirationAutorisation *time.Time `json:"date_expiration_autorisation"`
	StatutMateriel             string     `json:"statut_materiel"`
	SiteOrigine                string     `json:"site_origine"`
	Zones                      []string   `json:"zones"`
	HeureAutoriseeDebut        string     `json:"heure_autorisee_debut"` // "07:00"
	HeureAutoriseeFin          string     `json:"heure_autorisee_fin"`   // "18:00"
	AutoriseWeekend            bool       `json:"autorise_weekend"`
	DernierStatutFlux          string     `json:"dernier_statut_flux"`
	HorodatageDernierScan      *time.Time `json:"horodatage_dernier_scan"`
	Commentaire                string     `json:"commentaire"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

// ZonesLabel rend les zones sous forme lisible ("Zone-A, Zone-B").
func (d *Device) ZonesLabel() string {
	if len(d.Zones) == 0 {
		return ZoneAucune
	}
	out := ""
	for i, z := range d.Zones {
		if i > 0 {
			out += ", "
		}
		out += z
	}
	return out
}

// PlageHoraire rend "07:00 - 18:00".
func (d *Device) PlageHoraire() string {
	return d.HeureAutoriseeDebut + " - " + d.HeureAutoriseeFin
}

type AuditLog struct {
	ID             int64      `json:"id"`
	DeviceID       uuid.UUID  `json:"device_id"`
	NumeroSerie    string     `json:"numero_serie"`
	IDMateriel     string     `json:"id_materiel"`
	NomPrenom      string     `json:"nom_prenom"`
	ScannedAt      time.Time  `json:"scanned_at"`
	JourneeLogique time.Time  `json:"journee_logique"`
	SensSuggere    string     `json:"sens_suggere"`
	SensConfirme   string     `json:"sens_confirme"`
	Decision       string     `json:"decision"`
	AccessGranted  bool       `json:"access_granted"`
	Motifs         []string   `json:"motifs"`
	Derogation     bool       `json:"derogation"`
	Justification  string     `json:"justification"`
	PointControle  string     `json:"point_controle"`
	Zone           string     `json:"zone"`
	AgentID        *uuid.UUID `json:"agent_id,omitempty"`
	AgentNom       string     `json:"agent_nom"`
	IPAddress      string     `json:"ip_address"`
	UserAgent      string     `json:"user_agent"`
	Snapshot       []byte     `json:"-"`
}

type ImportLineError struct {
	Ligne   int    `json:"ligne"`
	Serie   string `json:"numero_serie"`
	Message string `json:"message"`
}

type ImportReport struct {
	ID           uuid.UUID         `json:"id"`
	NomFichier   string            `json:"nom_fichier"`
	LignesTotal  int               `json:"lignes_total"`
	LignesCreees int               `json:"lignes_creees"`
	LignesMajs   int               `json:"lignes_majs"`
	LignesErreur int               `json:"lignes_erreur"`
	Erreurs      []ImportLineError `json:"erreurs"`
	CreatedAt    time.Time         `json:"created_at"`
}

// ---------------------------------------------------------------------
// Validation des énumérations
// ---------------------------------------------------------------------

func ValidStatutAutorisation(v string) bool {
	switch v {
	case AutorisationAutorisee, AutorisationNonAutorisee, AutorisationSuspendue, AutorisationTemporaire:
		return true
	}
	return false
}

func ValidStatutMateriel(v string) bool {
	switch v {
	case MaterielActif, MaterielEnMaintenance, MaterielVolePerdu, MaterielReforme:
		return true
	}
	return false
}

func ValidSens(v string) bool {
	return v == FluxEntre || v == FluxSorti
}
