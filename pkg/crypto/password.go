package crypto

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

var ErrPasswordMismatch = errors.New("password does not match")

func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

func CheckPassword(hash, plain string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)); err != nil {
		return ErrPasswordMismatch
	}
	return nil
}
