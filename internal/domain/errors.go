package domain

import "errors"

var (
	ErrNotFound    = errors.New("introuvable")
	ErrAmbiguous   = errors.New("référence ambiguë")
	ErrDuplicate   = errors.New("existe déjà")
	ErrBadPassword = errors.New("mot de passe incorrect ou fichier corrompu")
)
