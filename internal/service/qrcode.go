package service

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/entreprise/device-auth/internal/config"
	"github.com/entreprise/device-auth/internal/domain"
	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
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

// ---------------------------------------------------------------------
// Étiquette composite : QR code 1.5x1.5cm + label gravé dans l'image
// ---------------------------------------------------------------------
//
// Utilisée partout où le fichier PNG peut circuler SANS le texte HTML
// d'accompagnement (téléchargement individuel, écriture sur disque) : sans
// cela, une fois le fichier sorti de l'application, plus aucun moyen de
// savoir à quel matériel il correspond à l'œil nu.
//
// La planche d'étiquettes imprimable (/admin/etiquettes) continue elle
// d'utiliser le QR nu (fonction PNG ci-dessus) : le texte y est déjà
// affiché à côté en HTML, plus grand et plus lisible qu'un texte gravé
// dans l'image.
const (
	// EtiquetteTailleCM est le côté du QR code, hors bandeau de texte.
	EtiquetteTailleCM = 1.5
	// EtiquetteDPI est la résolution d'impression visée. Pour que la taille
	// imprimée soit exactement 1.5x1.5cm, le logiciel/l'imprimante utilisée
	// doit imprimer ce fichier à cette même résolution. Les imprimantes
	// d'étiquettes thermiques courantes tournent plutôt à 203 DPI : changez
	// cette constante pour qu'elle corresponde à votre matériel.
	EtiquetteDPI = 300
)

// tailleQRPixels convertit EtiquetteTailleCM en pixels à EtiquetteDPI.
func tailleQRPixels() int {
	return int(math.Round(EtiquetteTailleCM / 2.54 * float64(EtiquetteDPI)))
}

// LabelPNG génère l'image composite : le QR code, exactement 1.5x1.5cm à
// EtiquetteDPI, surmonté d'un bandeau texte (par défaut le numéro de série).
// Le bandeau ajoute de la hauteur : SEUL le carré du QR mesure 1.5x1.5cm ;
// l'étiquette complète est un peu plus haute pour laisser la place au texte
// - un QR seul de 1.5cm sans marge n'a physiquement pas la place d'accueillir
// un texte lisible en dessous.

func (q *QRService) LabelPNG(serial, label string) ([]byte, error) {
	cote := tailleQRPixels()

	qr, err := qrcode.New(q.URLVerification(serial), qrcode.High)
	if err != nil {
		return nil, err
	}
	qrImg := qr.Image(cote)

	const bandeauHauteur = 34 // pixels réservés au texte, sous le QR

	texte := strings.ToUpper(strings.TrimSpace(label))
	if texte == "" {
		texte = strings.ToUpper(strings.TrimSpace(serial))
	}
	largeurTexte := font.MeasureString(basicfont.Face7x13, texte).Ceil()

	largeur := cote
	if largeurTexte+8 > largeur {
		// Un numéro de série long élargit le canevas plutôt que d'être coupé ;
		// le QR reste, lui, toujours exactement à 1.5x1.5cm.
		largeur = largeurTexte + 8
	}
	hauteur := cote + bandeauHauteur

	canevas := image.NewRGBA(image.Rect(0, 0, largeur, hauteur))
	draw.Draw(canevas, canevas.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)

	decalageX := (largeur - cote) / 2
	draw.Draw(canevas, image.Rect(decalageX, 0, decalageX+cote, cote), qrImg, image.Point{}, draw.Src)

	dessinateur := &font.Drawer{
		Dst:  canevas,
		Src:  image.NewUniform(color.Black),
		Face: basicfont.Face7x13,
	}
	dx := (largeur - largeurTexte) / 2
	if dx < 0 {
		dx = 0
	}
	dessinateur.Dot = fixed.Point26_6{X: fixed.I(dx), Y: fixed.I(cote + 20)}
	dessinateur.DrawString(texte)

	var buf bytes.Buffer
	if err := png.Encode(&buf, canevas); err != nil {
		return nil, fmt.Errorf("encodage PNG de l'étiquette: %w", err)
	}
	return buf.Bytes(), nil
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
