package domainerrors

import "errors"

var (
	ErrNotFound  = errors.New("resource not found")
	ErrConflict  = errors.New("resource conflict")
	ErrForbidden = errors.New("forbidden")
)
