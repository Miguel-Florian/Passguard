package service

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/entreprise/device-auth/internal/config"
	"github.com/entreprise/device-auth/internal/domain"
	"github.com/entreprise/device-auth/internal/repository"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
	"golang.org/x/text/unicode/norm"
)

type ImportService struct {
	devices *repository.DeviceRepo
	audits  *repository.AuditRepo
	cfg     *config.Config
}

func NewImportService(d *repository.DeviceRepo, a *repository.AuditRepo, cfg *config.Config) *ImportService {
	return &ImportService{devices: d, audits: a, cfg: cfg}
}

// Colonnes reconnues (la comparaison est insensible à la casse, aux accents,
// aux espaces et aux underscores : "Numéro de série" == "numero_serie").
const (
	colIDMateriel  = "idmateriel"
	colNumeroSerie = "numeroserie"
	colType        = "typemateriel"
	colMarque      = "marquemodele"
	colMatricule   = "matricule"
	colNom         = "nomprenom"
	colDepartement = "departement"
	colStatutAuth  = "statutautorisation"
	colDateExp     = "dateexpirationautorisation"
	colStatutMat   = "statutmateriel"
	colSite        = "siteorigine"
	colZones       = "zoneautorisee"
	colHDebut      = "heureautoriseedebut"
	colHFin        = "heureautoriseefin"
	colWeekend     = "autoriseweekend"
	colCommentaire = "commentaire"
)

// EnTetesModele est l'ordre des colonnes du fichier modèle généré.
var EnTetesModele = []string{
	"ID_Materiel", "Numero_Serie", "Type_Materiel", "Marque_Modele", "Matricule",
	"Nom_Prenom", "Departement", "Statut_Autorisation", "Date_Expiration_Autorisation",
	"Statut_Materiel", "Site_Origine", "Zone_Autorisee", "Heure_Autorisee_Debut",
	"Heure_Autorisee_Fin", "Autorise_Weekend", "Commentaire",
}

// Import lit le classeur et crée/met à jour les fiches.
//
// Note : les colonnes Dernier_Statut_Flux et Horodatage_Dernier_Scan
// présentes dans les exports existants sont volontairement IGNORÉES.
// Ces valeurs sont dérivées des scans ; elles ne peuvent pas être
// importées sans casser la piste d'audit.
func (s *ImportService) Import(ctx context.Context, r io.Reader, nomFichier string, adminID *uuid.UUID) (*domain.ImportReport, error) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return nil, fmt.Errorf("%w: fichier Excel illisible (%v)", domain.ErrValidation, err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("%w: classeur vide", domain.ErrValidation)
	}
	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("%w: aucune ligne de données", domain.ErrValidation)
	}

	// --- Cartographie des en-têtes ------------------------------------
	index := map[string]int{}
	for i, h := range rows[0] {
		key := normaliserCle(h)
		if key != "" {
			index[key] = i
		}
	}
	for _, requis := range []string{colNumeroSerie, colNom} {
		if _, ok := index[requis]; !ok {
			return nil, fmt.Errorf("%w: colonne obligatoire manquante (%s)", domain.ErrValidation, requis)
		}
	}

	rep := &domain.ImportReport{NomFichier: nomFichier, Erreurs: []domain.ImportLineError{}}
	vus := map[string]int{}

	for i := 1; i < len(rows); i++ {
		ligne := i + 1 // numéro affiché dans Excel
		row := rows[i]
		get := func(col string) string {
			if idx, ok := index[col]; ok && idx < len(row) {
				return strings.TrimSpace(row[idx])
			}
			return ""
		}

		serie := strings.ToUpper(get(colNumeroSerie))
		if serie == "" && get(colNom) == "" && get(colIDMateriel) == "" {
			continue // ligne vide
		}
		rep.LignesTotal++

		if serie == "" {
			rep.LignesErreur++
			rep.Erreurs = append(rep.Erreurs, domain.ImportLineError{
				Ligne: ligne, Message: "numéro de série manquant"})
			continue
		}
		if prec, dup := vus[serie]; dup {
			rep.LignesErreur++
			rep.Erreurs = append(rep.Erreurs, domain.ImportLineError{
				Ligne: ligne, Serie: serie,
				Message: fmt.Sprintf("doublon dans le fichier (déjà vu ligne %d)", prec)})
			continue
		}
		vus[serie] = ligne

		d, warn, err := s.construireDevice(get, serie)
		if err != nil {
			rep.LignesErreur++
			rep.Erreurs = append(rep.Erreurs, domain.ImportLineError{
				Ligne: ligne, Serie: serie, Message: err.Error()})
			continue
		}

		created, err := s.devices.Upsert(ctx, d)
		if err != nil {
			rep.LignesErreur++
			rep.Erreurs = append(rep.Erreurs, domain.ImportLineError{
				Ligne: ligne, Serie: serie, Message: err.Error()})
			continue
		}
		if created {
			rep.LignesCreees++
		} else {
			rep.LignesMajs++
		}
		for _, w := range warn {
			rep.Erreurs = append(rep.Erreurs, domain.ImportLineError{
				Ligne: ligne, Serie: serie, Message: "avertissement : " + w})
		}
	}

	if err := s.audits.SaveImportReport(ctx, adminID, rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// construireDevice traduit une ligne en entité, avec les corrections
// automatiques nécessaires (et les avertissements associés).
func (s *ImportService) construireDevice(get func(string) string, serie string) (*domain.Device, []string, error) {
	var warns []string

	nom := get(colNom)
	if nom == "" {
		return nil, nil, fmt.Errorf("détenteur (Nom_Prenom) manquant")
	}

	idMat := get(colIDMateriel)
	if idMat == "" {
		idMat = serie // à défaut, on retombe sur le S/N
		warns = append(warns, "ID_Materiel absent, le numéro de série a été utilisé")
	}

	typeMat := get(colType)
	if typeMat == "" {
		typeMat = "Non précisé"
	}

	statutAuth := normaliserEnum(get(colStatutAuth))
	statutMat := normaliserEnum(get(colStatutMat))

	// Cas connu du fichier historique : "TEMPORAIRE" placé dans la colonne
	// Statut_Materiel alors que c'est une notion d'habilitation.
	if statutMat == domain.AutorisationTemporaire {
		statutMat = domain.MaterielActif
		if statutAuth == "" || statutAuth == domain.AutorisationAutorisee {
			statutAuth = domain.AutorisationTemporaire
		}
		warns = append(warns, "Statut_Materiel=TEMPORAIRE reclassé en autorisation temporaire")
	}
	if statutAuth == "" {
		statutAuth = domain.AutorisationNonAutorisee
		warns = append(warns, "Statut_Autorisation absent, fiche créée en NON_AUTORISE")
	}
	if statutMat == "" {
		statutMat = domain.MaterielActif
	}
	if !domain.ValidStatutAutorisation(statutAuth) {
		return nil, nil, fmt.Errorf("Statut_Autorisation invalide: %s", statutAuth)
	}
	if !domain.ValidStatutMateriel(statutMat) {
		return nil, nil, fmt.Errorf("Statut_Materiel invalide: %s", statutMat)
	}

	var exp *time.Time
	if v := get(colDateExp); v != "" {
		t, err := parseDate(v, s.cfg.Loc)
		if err != nil {
			return nil, nil, fmt.Errorf("Date_Expiration_Autorisation illisible: %s", v)
		}
		exp = &t
	} else if statutAuth == domain.AutorisationTemporaire {
		return nil, nil, fmt.Errorf("une autorisation TEMPORAIRE exige une date d'expiration")
	}

	hDebut, err := parseHeureCell(get(colHDebut), "07:00")
	if err != nil {
		return nil, nil, fmt.Errorf("Heure_Autorisee_Debut illisible: %s", get(colHDebut))
	}
	hFin, err := parseHeureCell(get(colHFin), "18:00")
	if err != nil {
		return nil, nil, fmt.Errorf("Heure_Autorisee_Fin illisible: %s", get(colHFin))
	}

	zones := parseZones(get(colZones))
	if len(zones) == 0 {
		zones = []string{domain.ZoneAucune}
		warns = append(warns, "aucune zone renseignée, la fiche ne pourra pas sortir")
	}

	return &domain.Device{
		IDMateriel:                 idMat,
		NumeroSerie:                serie,
		TypeMateriel:               typeMat,
		MarqueModele:               get(colMarque),
		Matricule:                  get(colMatricule),
		NomPrenom:                  nom,
		Departement:                get(colDepartement),
		StatutAutorisation:         statutAuth,
		DateExpirationAutorisation: exp,
		StatutMateriel:             statutMat,
		SiteOrigine:                get(colSite),
		Zones:                      zones,
		HeureAutoriseeDebut:        hDebut,
		HeureAutoriseeFin:          hFin,
		AutoriseWeekend:            parseBool(get(colWeekend)),
		Commentaire:                get(colCommentaire),
	}, warns, nil
}

// ---------------------------------------------------------------------
// Génération du fichier modèle
// ---------------------------------------------------------------------

func (s *ImportService) Modele() ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Devices"
	idx, err := f.NewSheet(sheet)
	if err != nil {
		return nil, err
	}
	f.SetActiveSheet(idx)
	f.DeleteSheet("Sheet1")

	for i, h := range EnTetesModele {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	exemple := []any{
		"MAT-001", "SN-HP-889021", "Laptop", "HP EliteBook 840 G8", "EMP-102",
		"Jean Dupont", "Cybersécurité", "AUTORISE", "2027-12-31", "ACTIF",
		"HQ-Douala", "Zone-A, Zone-B", "07:00", "20:00", "OUI", "",
	}
	for i, v := range exemple {
		cell, _ := excelize.CoordinatesToCellName(i+1, 2)
		_ = f.SetCellValue(sheet, cell, v)
	}
	style, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4F46E5"}},
	})
	if err == nil {
		last, _ := excelize.CoordinatesToCellName(len(EnTetesModele), 1)
		_ = f.SetCellStyle(sheet, "A1", last, style)
	}
	_ = f.SetColWidth(sheet, "A", "P", 24)

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---------------------------------------------------------------------
// Parsing tolérant
// ---------------------------------------------------------------------

// normaliserCle met une en-tête sous forme canonique :
// "Date_Expiration Autorisation" -> "dateexpirationautorisation"
func normaliserCle(s string) string {
	s = sansAccents(strings.ToLower(strings.TrimSpace(s)))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// normaliserEnum : "non autorisé" -> "NON_AUTORISE"
func normaliserEnum(s string) string {
	s = sansAccents(strings.ToUpper(strings.TrimSpace(s)))
	s = strings.NewReplacer(" ", "_", "-", "_", "/", "_").Replace(s)
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	return s
}

func sansAccents(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func parseBool(s string) bool {
	switch normaliserEnum(s) {
	case "OUI", "YES", "TRUE", "VRAI", "1", "O", "Y", "X":
		return true
	}
	return false
}

func parseZones(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	repl := strings.NewReplacer(";", ",", "|", ",", "/", ",")
	parts := strings.Split(repl.Replace(s), ",")
	out := []string{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseDate accepte les formats texte usuels et les numéros de série Excel.
func parseDate(v string, loc *time.Location) (time.Time, error) {
	v = strings.TrimSpace(v)
	formats := []string{
		"2006-01-02", "02/01/2006", "2006/01/02", "02-01-2006",
		"2006-01-02 15:04:05", "01/02/06", "02.01.2006",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, v, loc); err == nil {
			return t, nil
		}
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
		if t, err := excelize.ExcelDateToTime(n, false); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc), nil
		}
	}
	return time.Time{}, fmt.Errorf("date illisible")
}

// parseHeureCell accepte "7:00", "07:00:00", "7h", ou une fraction Excel.
func parseHeureCell(v, def string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return def, nil
	}
	v = strings.ReplaceAll(strings.ToLower(v), "h", ":")
	v = strings.TrimSuffix(v, ":")

	for _, f := range []string{"15:04:05", "15:04", "3:04 PM", "15"} {
		if t, err := time.Parse(f, v); err == nil {
			return fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute()), nil
		}
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil && n >= 0 && n < 1 {
		total := int(n*24*60 + 0.5)
		return fmt.Sprintf("%02d:%02d", total/60, total%60), nil
	}
	return "", fmt.Errorf("heure illisible")
}
