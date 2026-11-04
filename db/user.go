package db

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// User represents user data.
type User struct {
	gorm.Model
	AccountID uint `gorm:"index" json:"account_id"`
}

var _ IDModel = &User{}

// Save performs the save operation.
func (u *User) Save(ctx context.Context) error {
	return DB().WithContext(ctx).Save(u).Error
}

// GetID returns id.
func (u *User) GetID() uint { return u.ID }

// LoadRelation loads relation.
func (u *User) LoadRelation(_ context.Context, _ ...string) {
}

// GetUser returns user.
func GetUser(ctx context.Context, uid uint) *User {
	u := &User{}
	DB().WithContext(ctx).Where("id", uid).First(u)
	return u
}

// RegisterUser performs the register user operation.
func RegisterUser(ctx context.Context, accountID uint) (*User, error) {
	u := &User{}
	err := DB().WithContext(ctx).Where("account_id = ?", accountID).First(u).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if u.ID > 0 {
		return u, nil
	}
	u.AccountID = accountID
	err = u.Save(ctx)
	if err != nil {
		return nil, err
	}
	return u, nil
}
