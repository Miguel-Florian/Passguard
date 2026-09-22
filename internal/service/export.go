package service

import (
	"strings"
	"time"

	"github.com/entreprise/device-auth/internal/domain"
	"github.com/xuri/excelize/v2"
)

// ExportDevices produit un classeur reprenant le format d'import.
func ExportDevices(devices []*domain.Device, loc *time.Location) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Devices"
	idx, err := f.NewSheet(sheet)
	if err != nil {
		return nil, err
	}
	f.SetActiveSheet(idx)
	f.DeleteSheet("Sheet1")

	entetes := append(append([]string{}, EnTetesModele...),
		"Dernier_Statut_Flux", "Horodatage_Dernier_Scan")
	if err := ecrireLigne(f, sheet, 1, toAny(entetes)); err != nil {
		return nil, err
	}

	for i, d := range devices {
		exp := ""
		if d.DateExpirationAutorisation != nil {
			exp = d.DateExpirationAutorisation.In(loc).Format("2006-01-02")
		}
		scan := ""
		if d.HorodatageDernierScan != nil {
			scan = d.HorodatageDernierScan.In(loc).Format("2006-01-02 15:04:05")
		}
		row := []any{
			d.IDMateriel, d.NumeroSerie, d.TypeMateriel, d.MarqueModele, d.Matricule,
			d.NomPrenom, d.Departement, d.StatutAutorisation, exp, d.StatutMateriel,
			d.SiteOrigine, d.ZonesLabel(), d.HeureAutoriseeDebut, d.HeureAutoriseeFin,
			boolLabel(d.AutoriseWeekend), d.Commentaire, d.DernierStatutFlux, scan,
		}
		if err := ecrireLigne(f, sheet, i+2, row); err != nil {
			return nil, err
		}
	}
	_ = f.SetColWidth(sheet, "A", "R", 22)

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ExportAudit produit le registre des mouvements, destiné aux contrôles.
func ExportAudit(logs []*domain.AuditLog, loc *time.Location) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Audit"
	idx, err := f.NewSheet(sheet)
	if err != nil {
		return nil, err
	}
	f.SetActiveSheet(idx)
	f.DeleteSheet("Sheet1")

	entetes := []string{
		"ID", "Horodatage", "Journee_Logique", "ID_Materiel", "Numero_Serie", "Detenteur",
		"Sens_Suggere", "Sens_Confirme", "Decision", "Acces_Accorde", "Motifs",
		"Derogation", "Justification", "Point_Controle", "Zone", "Agent", "IP",
	}
	if err := ecrireLigne(f, sheet, 1, toAny(entetes)); err != nil {
		return nil, err
	}

	for i, l := range logs {
		row := []any{
			l.ID,
			l.ScannedAt.In(loc).Format("2006-01-02 15:04:05"),
			l.JourneeLogique.In(loc).Format("2006-01-02"),
			l.IDMateriel, l.NumeroSerie, l.NomPrenom,
			l.SensSuggere, l.SensConfirme, l.Decision, boolLabel(l.AccessGranted),
			strings.Join(l.Motifs, " | "), boolLabel(l.Derogation), l.Justification,
			l.PointControle, l.Zone, l.AgentNom, l.IPAddress,
		}
		if err := ecrireLigne(f, sheet, i+2, row); err != nil {
			return nil, err
		}
	}
	_ = f.SetColWidth(sheet, "A", "Q", 20)

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ecrireLigne(f *excelize.File, sheet string, ligne int, valeurs []any) error {
	for i, v := range valeurs {
		cell, err := excelize.CoordinatesToCellName(i+1, ligne)
		if err != nil {
			return err
		}
		if err := f.SetCellValue(sheet, cell, v); err != nil {
			return err
		}
	}
	return nil
}

func toAny(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}
