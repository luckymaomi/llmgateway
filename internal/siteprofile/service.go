package siteprofile

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/luckymaomi/llmgateway/internal/identity"
)

type Service struct {
	repository Repository
}

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("site profile repository is required")
	}
	return &Service{repository: repository}, nil
}

func (s *Service) Get(ctx context.Context) (Profile, error) {
	return s.repository.Get(ctx)
}

func (s *Service) Update(ctx context.Context, actor identity.Principal, update Update) (Profile, error) {
	if !actor.CanManageUsers() {
		return Profile{}, ErrForbidden
	}
	update.Name = strings.TrimSpace(update.Name)
	update.Description = strings.TrimSpace(update.Description)
	update.Contact = strings.TrimSpace(update.Contact)
	update.ActorID = actor.UserID
	if update.ExpectedVersion < 1 || update.RequestID == "" || len(update.RequestID) > 128 ||
		utf8.RuneCountInString(update.Name) < 2 || utf8.RuneCountInString(update.Name) > 80 ||
		utf8.RuneCountInString(update.Description) > 240 || utf8.RuneCountInString(update.Contact) > 200 {
		return Profile{}, ErrInvalidInput
	}
	return s.repository.Update(ctx, update)
}
