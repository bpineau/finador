package domain

import "errors"

var (
	ErrNotFound    = errors.New("not found")
	ErrAmbiguous   = errors.New("ambiguous reference")
	ErrDuplicate   = errors.New("already exists")
	ErrBadPassword = errors.New("wrong password or corrupted file")
)
