package domain

import "errors"

var (
	ErrNotFound           = errors.New("ressource introuvable")
	ErrDuplicate          = errors.New("ressource déjà existante")
	ErrInvalidCredentials = errors.New("identifiants invalides")
	ErrForbidden          = errors.New("action non autorisée")
	ErrValidation         = errors.New("données invalides")
	ErrDerogationRefusee  = errors.New("dérogation impossible pour ce scan")
)
