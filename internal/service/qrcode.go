package service

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/entreprise/device-auth/internal/config"
	"github.com/entreprise/device-auth/internal/domain"
	qrcode "github.com/skip2/go-qrcode"
)

type QRService struct {
	cfg *config.Config
}

func NewQRService(cfg *config.Config) *QRService { return &QRService{cfg: cfg} }

// URLVerification construit l'URL encodée dans le QR code.
// Le QR contient le numéro de série, mais sous forme d'URL : n'importe
// quel appareil photo de téléphone ouvre directement la page de contrôle.
func (q *QRService) URLVerification(serial string) string {
	return q.cfg.BaseURL + "/v/" + url.PathEscape(strings.TrimSpace(serial))
}

// PNG génère l'image du QR code. taille = côté en pixels (256 par défaut).
func (q *QRService) PNG(serial string, taille int) ([]byte, error) {
	if taille <= 0 || taille > 1024 {
		taille = 256
	}
	// Niveau de correction élevé : l'étiquette reste lisible même abîmée.
	return qrcode.Encode(q.URLVerification(serial), qrcode.High, taille)
}

// DataURI renvoie le QR code prêt à être inséré dans une balise <img>.
func (q *QRService) DataURI(serial string, taille int) (string, error) {
	png, err := q.PNG(serial, taille)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

// Etiquette représente une vignette de la planche à imprimer.
type Etiquette struct {
	IDMateriel  string
	NumeroSerie string
	Type        string
	Modele      string
	Detenteur   string
	QRDataURI   string
	URL         string
}

// ---------------------------------------------------------------------
// Écriture sur disque (dossier interne au serveur, cf. config.QRCodesDir)
// ---------------------------------------------------------------------

// nomFichierAutorise ne garde que des caractères sûrs pour un nom de fichier,
// afin qu'un numéro de série fantaisiste ne puisse jamais sortir du dossier
// cible (pas de "/", "..", espace, etc.).
var nomFichierAutorise = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func nomFichierQR(serial string) string {
	nom := nomFichierAutorise.ReplaceAllString(strings.ToUpper(strings.TrimSpace(serial)), "_")
	if nom == "" {
		nom = "SANS_SERIE"
	}
	return nom + ".png"
}

// RegenerateAllResult résume l'opération de régénération en masse.
type RegenerateAllResult struct {
	Dossier string   `json:"dossier"`
	Generes int      `json:"generes"`
	Total   int      `json:"total"`
	Erreurs []string `json:"erreurs"`
}

// RegenerateAll (re)génère un fichier PNG par device dans config.QRCodesDir.
// Le dossier est créé s'il n'existe pas ; les fichiers déjà présents pour
// des matériels toujours actifs sont écrasés (le contenu suit toujours le
// BASE_URL courant). Rien n'est fait au démarrage ni à l'import : cette
// méthode n'est appelée que depuis le bouton "Régénérer tous les QR codes"
// de la console d'administration.
func (q *QRService) RegenerateAll(devices []*domain.Device) (*RegenerateAllResult, error) {
	dir := strings.TrimSpace(q.cfg.QRCodesDir)
	if dir == "" {
		dir = "./qr_codes"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("création du dossier %s impossible: %w", dir, err)
	}

	res := &RegenerateAllResult{Dossier: dir, Total: len(devices), Erreurs: []string{}}
	for _, d := range devices {
		png, err := q.PNG(d.NumeroSerie, 512)
		if err != nil {
			res.Erreurs = append(res.Erreurs, d.NumeroSerie+": "+err.Error())
			continue
		}
		chemin := filepath.Join(dir, nomFichierQR(d.NumeroSerie))
		if err := os.WriteFile(chemin, png, 0o644); err != nil {
			res.Erreurs = append(res.Erreurs, d.NumeroSerie+": "+err.Error())
			continue
		}
		res.Generes++
	}
	return res, nil
}

func (q *QRService) Etiquettes(devices []*domain.Device) ([]Etiquette, error) {
	out := make([]Etiquette, 0, len(devices))
	for _, d := range devices {
		uri, err := q.DataURI(d.NumeroSerie, 220)
		if err != nil {
			return nil, err
		}
		out = append(out, Etiquette{
			IDMateriel:  d.IDMateriel,
			NumeroSerie: d.NumeroSerie,
			Type:        d.TypeMateriel,
			Modele:      d.MarqueModele,
			Detenteur:   d.NomPrenom,
			QRDataURI:   uri,
			URL:         q.URLVerification(d.NumeroSerie),
		})
	}
	return out, nil
}
