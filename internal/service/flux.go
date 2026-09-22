package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/entreprise/device-auth/internal/domain"
)

// =====================================================================
// MOTEUR DE DÉCISION
// ---------------------------------------------------------------------
// Ce fichier est volontairement sans dépendance à la base de données :
// ce sont des fonctions pures, donc testables unitairement (flux_test.go).
// =====================================================================

// FluxOptions porte les paramètres de configuration nécessaires au moteur.
type FluxOptions struct {
	PivotHour         int            // début de la "journée logique" (5 => 05:00)
	DerogationMaxHour int            // heure limite d'une dérogation (21 => 21:00)
	Loc               *time.Location // fuseau de référence
}

// ScanInput représente la demande de scan.
type ScanInput struct {
	Sens          string // ENTRE | SORTI | "" (=> le serveur suggère)
	Zone          string // zone du point de contrôle, optionnel
	Derogation    bool   // le vigile demande une dérogation
	Justification string
	Now           time.Time
}

// RuleCheck est le résultat d'une règle unitaire, tel qu'affiché dans la
// matrice "Évaluation des Règles du Serveur" de la page guérite.
type RuleCheck struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Valid bool   `json:"valid"`
}

// Evaluation est le verdict complet du moteur.
type Evaluation struct {
	Decision            string      `json:"decision"` // AUTORISE | REFUSE | ALERTE
	Code                string      `json:"code"`     // code machine, ex: AUTHORIZED_OUT
	Title               string      `json:"title"`    // titre affiché
	Message             string      `json:"message"`  // message affiché
	Color               string      `json:"color"`    // GREEN | RED | AMBER
	AccessGranted       bool        `json:"access_granted"`
	Motifs              []string    `json:"motifs"`
	MotifsLabels        []string    `json:"motifs_labels"`
	SensSuggere         string      `json:"sens_suggere"`
	SensApplique        string      `json:"sens_applique"`
	SensLabel           string      `json:"sens_label"` // ex: "ENTRÉE ➔ SORTIE"
	Anomalie            bool        `json:"anomalie"`
	DerogationPossible  bool        `json:"derogation_possible"`
	DerogationAppliquee bool        `json:"derogation_appliquee"`
	Rules               []RuleCheck `json:"rules"`
	JourneeLogique      time.Time   `json:"journee_logique"`
	Horodatage          time.Time   `json:"horodatage"`
}

// ---------------------------------------------------------------------
// Journée logique
// ---------------------------------------------------------------------

// JourneeLogique renvoie la date de rattachement d'un instant donné.
// Avec un pivot à 05:00, un scan du 15/09 à 02:30 appartient au 14/09 :
// c'est ce qui permet de gérer les équipes qui finissent à 21h et les
// retours tardifs sans casser l'alternance entrée/sortie.
func JourneeLogique(t time.Time, pivotHour int, loc *time.Location) time.Time {
	lt := t.In(loc)
	if lt.Hour() < pivotHour {
		lt = lt.AddDate(0, 0, -1)
	}
	return time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
}

// ---------------------------------------------------------------------
// Sens suggéré (alternance)
// ---------------------------------------------------------------------

// SuggererSens calcule le sens probable du mouvement par alternance à
// partir du dernier état connu du device.
//
// Règles :
//   - jamais scanné            -> ENTRE
//   - dernier = SORTI          -> ENTRE (le matériel revient)
//   - dernier = ENTRE, même journée logique -> SORTI
//   - dernier = ENTRE, journée antérieure   -> ENTRE + ANOMALIE
//     (le matériel est physiquement sorti sans avoir été scanné)
//
// Le booléen renvoyé signale l'anomalie.
func SuggererSens(d *domain.Device, now time.Time, opt FluxOptions) (string, bool) {
	if d.HorodatageDernierScan == nil || d.DernierStatutFlux == domain.FluxInconnu {
		return domain.FluxEntre, false
	}

	switch d.DernierStatutFlux {
	case domain.FluxSorti:
		return domain.FluxEntre, false
	case domain.FluxEntre:
		jCourante := JourneeLogique(now, opt.PivotHour, opt.Loc)
		jDernier := JourneeLogique(*d.HorodatageDernierScan, opt.PivotHour, opt.Loc)
		if jDernier.Equal(jCourante) {
			return domain.FluxSorti, false
		}
		// Dernier scan = entrée, mais d'une journée passée : le matériel a
		// quitté le bâtiment sans passer par la guérite.
		return domain.FluxEntre, true
	}
	return domain.FluxEntre, false
}

// ---------------------------------------------------------------------
// Évaluation complète
// ---------------------------------------------------------------------

// Evaluer applique la totalité des règles et rend le verdict.
func Evaluer(d *domain.Device, in ScanInput, opt FluxOptions) Evaluation {
	now := in.Now.In(opt.Loc)

	sensSuggere, anomalie := SuggererSens(d, now, opt)
	sens := strings.ToUpper(strings.TrimSpace(in.Sens))
	if !domain.ValidSens(sens) {
		sens = sensSuggere
	}

	ev := Evaluation{
		SensSuggere:    sensSuggere,
		SensApplique:   sens,
		Anomalie:       anomalie,
		Motifs:         []string{},
		JourneeLogique: JourneeLogique(now, opt.PivotHour, opt.Loc),
		Horodatage:     now,
	}
	if anomalie {
		ev.Motifs = append(ev.Motifs, domain.MotifAnomalieFlux)
	}

	// --- Règle 1 : état du matériel ----------------------------------
	materielOK := d.StatutMateriel == domain.MaterielActif
	alerte := false
	switch d.StatutMateriel {
	case domain.MaterielVolePerdu:
		ev.Motifs = append(ev.Motifs, domain.MotifMaterielVolePerdu)
		alerte = true
	case domain.MaterielEnMaintenance:
		ev.Motifs = append(ev.Motifs, domain.MotifMaterielEnMaintenance)
	case domain.MaterielReforme:
		ev.Motifs = append(ev.Motifs, domain.MotifMaterielReforme)
	}

	// --- Règle 2 : habilitation --------------------------------------
	autorisationOK := true
	switch d.StatutAutorisation {
	case domain.AutorisationNonAutorisee:
		autorisationOK = false
		ev.Motifs = append(ev.Motifs, domain.MotifNonAutorise)
	case domain.AutorisationSuspendue:
		autorisationOK = false
		ev.Motifs = append(ev.Motifs, domain.MotifSuspendu)
	}

	// --- Règle 3 : expiration ----------------------------------------
	if d.DateExpirationAutorisation != nil {
		exp := *d.DateExpirationAutorisation
		jour := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, opt.Loc)
		expJour := time.Date(exp.Year(), exp.Month(), exp.Day(), 0, 0, 0, 0, opt.Loc)
		if expJour.Before(jour) {
			autorisationOK = false
			ev.Motifs = append(ev.Motifs, domain.MotifExpire)
		}
	} else if d.StatutAutorisation == domain.AutorisationTemporaire {
		// Une autorisation temporaire sans date de fin n'a pas de sens.
		autorisationOK = false
		ev.Motifs = append(ev.Motifs, domain.MotifExpire)
	}

	// --- Règle 4 : plage horaire -------------------------------------
	horaireOK, apresFin := DansPlageHoraire(now, d.HeureAutoriseeDebut, d.HeureAutoriseeFin, opt.Loc)
	if !horaireOK {
		ev.Motifs = append(ev.Motifs, domain.MotifHorsPlageHoraire)
	}

	// --- Règle 5 : week-end ------------------------------------------
	weekend := now.Weekday() == time.Saturday || now.Weekday() == time.Sunday
	weekendOK := !weekend || d.AutoriseWeekend
	if !weekendOK {
		ev.Motifs = append(ev.Motifs, domain.MotifWeekendNonAutorise)
	}

	// --- Règle 6 : zone ----------------------------------------------
	zoneOK := ZoneAutorisee(d.Zones, in.Zone)
	if !zoneOK {
		ev.Motifs = append(ev.Motifs, domain.MotifZoneNonAutorisee)
	}

	// --- Matrice affichée --------------------------------------------
	ev.Rules = []RuleCheck{
		{Label: "Habilitation", Value: libelleAutorisation(d, autorisationOK), Valid: autorisationOK},
		{Label: "Plage horaire", Value: d.PlageHoraire(), Valid: horaireOK},
		{Label: "Week-end", Value: boolLabel(d.AutoriseWeekend), Valid: weekendOK},
		{Label: "Statut fiche", Value: d.StatutMateriel, Valid: materielOK},
		{Label: "Zones", Value: d.ZonesLabel(), Valid: zoneOK},
	}

	// --- Décision ----------------------------------------------------
	//
	// Une ENTRÉE n'est jamais bloquée : un matériel qui rentre ne présente
	// aucun risque, on veut au contraire l'enregistrer. Seul un matériel
	// déclaré volé/perdu déclenche une ALERTE (il faut le retenir).
	if sens == domain.FluxEntre {
		if alerte {
			ev.Decision = domain.DecisionAlerte
			ev.Code = "ALERT_STOLEN_IN"
			ev.Title = "ALERTE - MATÉRIEL SIGNALÉ"
			ev.Message = "Ce matériel est déclaré volé ou perdu. Retenir l'équipement et prévenir immédiatement la sécurité."
			ev.Color = "AMBER"
			ev.AccessGranted = false
		} else {
			ev.Decision = domain.DecisionAutorise
			ev.Code = "AUTHORIZED_IN"
			ev.Title = "ENTRÉE ENREGISTRÉE"
			ev.Message = "Le matériel entre dans l'établissement. Mouvement enregistré."
			ev.Color = "GREEN"
			ev.AccessGranted = true
		}
		ev.SensLabel = sensLabel(d.DernierStatutFlux, sens, ev.AccessGranted)
		ev.MotifsLabels = labelsMotifs(ev.Motifs)
		return ev
	}

	// --- Cas d'une SORTIE : toutes les règles s'appliquent ------------
	bloquants := motifsBloquants(ev.Motifs)

	// La dérogation n'est envisageable que si le SEUL blocage est l'horaire,
	// que le dépassement est en fin de journée (pas avant l'heure de début)
	// et que l'heure limite de dérogation n'est pas franchie.
	ev.DerogationPossible = len(bloquants) == 1 &&
		bloquants[0] == domain.MotifHorsPlageHoraire &&
		apresFin &&
		now.Hour() < opt.DerogationMaxHour

	if len(bloquants) == 0 {
		ev.Decision = domain.DecisionAutorise
		ev.Code = "AUTHORIZED_OUT"
		ev.Title = "SORTIE AUTORISÉE"
		ev.Message = "Le matériel peut quitter l'établissement avec son détenteur."
		ev.Color = "GREEN"
		ev.AccessGranted = true
	} else if in.Derogation && ev.DerogationPossible && strings.TrimSpace(in.Justification) != "" {
		ev.Decision = domain.DecisionAutorise
		ev.Code = "AUTHORIZED_OUT_DEROGATION"
		ev.Title = "SORTIE AUTORISÉE (DÉROGATION)"
		ev.Message = fmt.Sprintf("Sortie hors plage horaire autorisée par dérogation, jusqu'à %02d:00.", opt.DerogationMaxHour)
		ev.Color = "AMBER"
		ev.AccessGranted = true
		ev.DerogationAppliquee = true
		ev.Motifs = append(ev.Motifs, domain.MotifDerogationAccordee)
	} else if alerte {
		ev.Decision = domain.DecisionAlerte
		ev.Code = "ALERT_STOLEN_OUT"
		ev.Title = "ALERTE - MATÉRIEL SIGNALÉ"
		ev.Message = "Matériel déclaré volé ou perdu. Sortie interdite : retenir l'équipement et prévenir la sécurité."
		ev.Color = "AMBER"
		ev.AccessGranted = false
	} else {
		ev.Decision = domain.DecisionRefuse
		ev.Code = "ACCESS_DENIED"
		ev.Title = "ACCÈS REFUSÉ"
		ev.Message = "Sortie interdite : " + strings.Join(labelsMotifs(bloquants), " ; ") + "."
		ev.Color = "RED"
		ev.AccessGranted = false
	}

	ev.SensLabel = sensLabel(d.DernierStatutFlux, sens, ev.AccessGranted)
	ev.MotifsLabels = labelsMotifs(ev.Motifs)
	return ev
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

// motifsBloquants filtre les motifs purement informatifs.
func motifsBloquants(motifs []string) []string {
	out := make([]string, 0, len(motifs))
	for _, m := range motifs {
		if m == domain.MotifAnomalieFlux || m == domain.MotifDerogationAccordee {
			continue // signalé, mais ne bloque pas à lui seul
		}
		out = append(out, m)
	}
	return out
}

func labelsMotifs(motifs []string) []string {
	out := make([]string, 0, len(motifs))
	for _, m := range motifs {
		out = append(out, domain.LibelleMotif(m))
	}
	return out
}

// DansPlageHoraire indique si l'heure courante est dans la fenêtre
// autorisée. Le second retour indique si l'on est APRÈS l'heure de fin
// (par opposition à AVANT l'heure de début) : seul ce cas ouvre droit à
// une dérogation de fin de journée.
func DansPlageHoraire(now time.Time, debut, fin string, loc *time.Location) (bool, bool) {
	hd, err1 := parseHeure(debut)
	hf, err2 := parseHeure(fin)
	if err1 != nil || err2 != nil {
		return true, false // configuration illisible : on ne bloque pas
	}
	cur := now.Hour()*60 + now.Minute()

	if hd <= hf {
		// Fenêtre classique : 07:00 - 18:00
		if cur < hd {
			return false, false
		}
		if cur > hf {
			return false, true
		}
		return true, false
	}
	// Fenêtre qui traverse minuit : 22:00 - 06:00
	if cur >= hd || cur <= hf {
		return true, false
	}
	return false, true
}

func parseHeure(s string) (int, error) {
	s = strings.TrimSpace(s)
	if len(s) > 5 {
		s = s[:5]
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

// ZoneAutorisee vérifie la couverture de la zone du point de contrôle.
// Une zone de contrôle vide (non renseignée) n'est pas discriminante,
// mais un device sans aucune zone est toujours refusé.
func ZoneAutorisee(zones []string, zoneControle string) bool {
	if len(zones) == 0 {
		return false
	}
	for _, z := range zones {
		if strings.EqualFold(strings.TrimSpace(z), domain.ZoneAucune) {
			return false
		}
	}
	for _, z := range zones {
		if strings.EqualFold(strings.TrimSpace(z), domain.ZoneToutes) {
			return true
		}
	}
	if strings.TrimSpace(zoneControle) == "" {
		return true
	}
	for _, z := range zones {
		if strings.EqualFold(strings.TrimSpace(z), strings.TrimSpace(zoneControle)) {
			return true
		}
	}
	return false
}

func libelleAutorisation(d *domain.Device, ok bool) string {
	if ok {
		if d.StatutAutorisation == domain.AutorisationTemporaire {
			return "Temporaire (valide)"
		}
		return "Valide"
	}
	switch d.StatutAutorisation {
	case domain.AutorisationSuspendue:
		return "Suspendue"
	case domain.AutorisationNonAutorisee:
		return "Non autorisé"
	}
	return "Expirée"
}

func boolLabel(b bool) string {
	if b {
		return "OUI"
	}
	return "NON"
}

// sensLabel produit l'affichage "ENTRÉE ➔ SORTIE" de la page guérite.
func sensLabel(precedent, sens string, granted bool) string {
	fr := map[string]string{
		domain.FluxEntre:   "ENTRÉE",
		domain.FluxSorti:   "SORTIE",
		domain.FluxInconnu: "INCONNU",
	}
	if !granted {
		if sens == domain.FluxSorti {
			return "BLOQUÉ (tentative de sortie)"
		}
		return "BLOQUÉ (tentative d'entrée)"
	}
	p, ok := fr[precedent]
	if !ok {
		p = "INCONNU"
	}
	return p + " ➔ " + fr[sens]
}
