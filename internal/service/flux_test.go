package service

import (
	"testing"
	"time"

	"github.com/entreprise/device-auth/internal/domain"
)

func opts() FluxOptions {
	loc, _ := time.LoadLocation("UTC")
	return FluxOptions{PivotHour: 5, DerogationMaxHour: 21, Loc: loc}
}

func deviceOK() *domain.Device {
	return &domain.Device{
		IDMateriel:          "MAT-001",
		NumeroSerie:         "SN-HP-889021",
		TypeMateriel:        "Laptop",
		NomPrenom:           "Jean Dupont",
		StatutAutorisation:  domain.AutorisationAutorisee,
		StatutMateriel:      domain.MaterielActif,
		Zones:               []string{"Zone-A", "Zone-B"},
		HeureAutoriseeDebut: "07:00",
		HeureAutoriseeFin:   "18:00",
		AutoriseWeekend:     true,
		DernierStatutFlux:   domain.FluxInconnu,
	}
}

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestJourneeLogique(t *testing.T) {
	o := opts()
	// 02:30 appartient à la journée précédente (pivot 05:00)
	j := JourneeLogique(at("2026-09-15 02:30"), o.PivotHour, o.Loc)
	if j.Day() != 14 {
		t.Fatalf("attendu 14, obtenu %d", j.Day())
	}
	j = JourneeLogique(at("2026-09-15 20:45"), o.PivotHour, o.Loc)
	if j.Day() != 15 {
		t.Fatalf("attendu 15, obtenu %d", j.Day())
	}
}

func TestSensAlternance(t *testing.T) {
	o := opts()
	d := deviceOK()

	// Jamais scanné -> ENTRE
	if s, _ := SuggererSens(d, at("2026-09-15 08:00"), o); s != domain.FluxEntre {
		t.Fatalf("attendu ENTRE, obtenu %s", s)
	}

	// Entré ce matin -> la suggestion suivante est SORTI
	last := at("2026-09-15 08:00")
	d.DernierStatutFlux = domain.FluxEntre
	d.HorodatageDernierScan = &last
	if s, _ := SuggererSens(d, at("2026-09-15 17:30"), o); s != domain.FluxSorti {
		t.Fatalf("attendu SORTI, obtenu %s", s)
	}

	// Sorti hier soir -> revient aujourd'hui
	last2 := at("2026-09-14 19:00")
	d.DernierStatutFlux = domain.FluxSorti
	d.HorodatageDernierScan = &last2
	if s, anomalie := SuggererSens(d, at("2026-09-15 07:30"), o); s != domain.FluxEntre || anomalie {
		t.Fatalf("attendu ENTRE sans anomalie, obtenu %s / %v", s, anomalie)
	}

	// Entré hier et jamais ressorti -> anomalie détectée
	last3 := at("2026-09-14 08:00")
	d.DernierStatutFlux = domain.FluxEntre
	d.HorodatageDernierScan = &last3
	if s, anomalie := SuggererSens(d, at("2026-09-15 07:30"), o); s != domain.FluxEntre || !anomalie {
		t.Fatalf("anomalie de flux non détectée (%s / %v)", s, anomalie)
	}
}

func TestSortieAutorisee(t *testing.T) {
	d := deviceOK()
	ev := Evaluer(d, ScanInput{Sens: domain.FluxSorti, Now: at("2026-09-15 17:00")}, opts())
	if !ev.AccessGranted || ev.Decision != domain.DecisionAutorise {
		t.Fatalf("sortie devrait être autorisée: %+v", ev)
	}
}

func TestSortieHorsPlageAvecDerogation(t *testing.T) {
	d := deviceOK()
	o := opts()
	now := at("2026-09-15 19:30") // après 18:00, avant 21:00

	ev := Evaluer(d, ScanInput{Sens: domain.FluxSorti, Now: now}, o)
	if ev.AccessGranted {
		t.Fatal("la sortie hors plage ne doit pas être accordée sans dérogation")
	}
	if !ev.DerogationPossible {
		t.Fatal("la dérogation devrait être proposée")
	}

	ev = Evaluer(d, ScanInput{Sens: domain.FluxSorti, Now: now, Derogation: true,
		Justification: "Astreinte projet Alpha"}, o)
	if !ev.AccessGranted || !ev.DerogationAppliquee {
		t.Fatalf("la dérogation aurait dû être appliquée: %+v", ev)
	}
}

func TestPasDeDerogationApresHeureLimite(t *testing.T) {
	d := deviceOK()
	ev := Evaluer(d, ScanInput{Sens: domain.FluxSorti, Now: at("2026-09-15 22:10")}, opts())
	if ev.DerogationPossible {
		t.Fatal("aucune dérogation ne doit être possible après 21:00")
	}
}

func TestPasDeDerogationSiAutreBlocage(t *testing.T) {
	d := deviceOK()
	d.StatutAutorisation = domain.AutorisationNonAutorisee
	ev := Evaluer(d, ScanInput{Sens: domain.FluxSorti, Now: at("2026-09-15 19:30"),
		Derogation: true, Justification: "test"}, opts())
	if ev.AccessGranted || ev.DerogationPossible {
		t.Fatal("la dérogation ne couvre que le dépassement horaire")
	}
}

func TestMaterielVoleDeclencheAlerte(t *testing.T) {
	d := deviceOK()
	d.StatutMateriel = domain.MaterielVolePerdu
	ev := Evaluer(d, ScanInput{Sens: domain.FluxSorti, Now: at("2026-09-15 10:00")}, opts())
	if ev.Decision != domain.DecisionAlerte || ev.AccessGranted {
		t.Fatalf("attendu une ALERTE bloquante: %+v", ev)
	}
}

func TestEntreeToujoursEnregistree(t *testing.T) {
	d := deviceOK()
	d.StatutAutorisation = domain.AutorisationNonAutorisee
	ev := Evaluer(d, ScanInput{Sens: domain.FluxEntre, Now: at("2026-09-15 22:00")}, opts())
	if !ev.AccessGranted {
		t.Fatal("une entrée ne doit jamais être bloquée pour un motif d'habilitation")
	}
}

func TestWeekendRefuse(t *testing.T) {
	d := deviceOK()
	d.AutoriseWeekend = false
	// 2026-09-19 est un samedi
	ev := Evaluer(d, ScanInput{Sens: domain.FluxSorti, Now: at("2026-09-19 10:00")}, opts())
	if ev.AccessGranted {
		t.Fatal("sortie week-end interdite pour ce device")
	}
}

func TestZones(t *testing.T) {
	if ZoneAutorisee([]string{"Zone-Toutes"}, "Zone-C") != true {
		t.Fatal("Zone-Toutes doit tout couvrir")
	}
	if ZoneAutorisee([]string{"Aucune"}, "") != false {
		t.Fatal("Aucune doit toujours refuser")
	}
	if ZoneAutorisee([]string{"Zone-A"}, "Zone-B") != false {
		t.Fatal("Zone-B ne doit pas être couverte")
	}
}
