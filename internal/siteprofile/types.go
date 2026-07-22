package siteprofile

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidInput = errors.New("invalid site profile input")
	ErrForbidden    = errors.New("site profile operation forbidden")
	ErrConflict     = errors.New("site profile update conflict")
)

type Profile struct {
	Name        string
	Description string
	Contact     string
	Version     int64
	UpdatedAt   time.Time
}

type Update struct {
	Name            string
	Description     string
	Contact         string
	ExpectedVersion int64
	RequestID       string
	ActorID         uuid.UUID
}

type Repository interface {
	Get(context.Context) (Profile, error)
	Update(context.Context, Update) (Profile, error)
}
