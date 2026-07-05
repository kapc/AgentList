package auth

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrWeakPassword = errors.New("password too short")
	ErrInvalidEmail = errors.New("invalid email")
)

func NormalizeEmail(email string) (string, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return "", ErrInvalidEmail
	}
	return addr.Address, nil
}

func ValidatePassword(password string, minLen int) error {
	if len(password) < minLen {
		return fmt.Errorf("%w: minimum %d characters", ErrWeakPassword, minLen)
	}
	return nil
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func SlugFromID(id string) string {
	s := strings.ReplaceAll(id, "-", "")
	if len(s) > 12 {
		s = s[:12]
	}
	return strings.ToLower(s)
}