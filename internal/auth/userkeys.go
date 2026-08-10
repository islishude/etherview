package auth

import (
	"context"
	"errors"
	"time"
)

type UserKeyPageAfter struct {
	CreatedAt time.Time
	Prefix    string
}

type UserRepository interface {
	PutForUser(context.Context, string, APIKey, int) error
	UserKey(context.Context, string, string) (APIKey, error)
	RotateForUser(context.Context, string, string, APIKey) error
	RevokeForUser(context.Context, string, string, time.Time) (APIKey, error)
	ListForUser(context.Context, string, *UserKeyPageAfter, int) ([]APIKey, int, error)
}

type UserKeyPage struct {
	Items       []APIKey
	ActiveCount int
}

type UserKeyPolicy struct {
	Rate          int
	Burst         int
	MaximumActive int
}

type UserService struct {
	manager    Manager
	repository UserRepository
	policy     UserKeyPolicy
}

func NewUserService(manager Manager, policy UserKeyPolicy) (*UserService, error) {
	repository, ok := manager.Repository.(UserRepository)
	if !ok {
		return nil, errors.New("user API key repository is required")
	}
	if policy.Rate < 1 || policy.Burst < policy.Rate || policy.MaximumActive < 1 {
		return nil, errors.New("user API key policy is invalid")
	}
	return &UserService{manager: manager, repository: repository, policy: policy}, nil
}

func (service *UserService) Policy() UserKeyPolicy { return service.policy }

func (service *UserService) Create(
	ctx context.Context,
	userID, name string,
	scopes []Scope,
) (IssuedAPIKey, error) {
	return service.manager.CreateForUser(
		ctx, userID, name, scopes, service.policy.Rate, service.policy.Burst,
		service.policy.MaximumActive,
	)
}

func (service *UserService) Rotate(
	ctx context.Context,
	userID, prefix string,
) (IssuedAPIKey, error) {
	return service.manager.RotateForUser(ctx, userID, prefix)
}

func (service *UserService) Revoke(
	ctx context.Context,
	userID, prefix string,
	revokedAt time.Time,
) error {
	_, err := service.repository.RevokeForUser(ctx, userID, prefix, revokedAt)
	return err
}

func (service *UserService) List(
	ctx context.Context,
	userID string,
	after *UserKeyPageAfter,
	limit int,
) (UserKeyPage, error) {
	items, active, err := service.repository.ListForUser(ctx, userID, after, limit)
	if err != nil {
		return UserKeyPage{}, err
	}
	return UserKeyPage{Items: items, ActiveCount: active}, nil
}
