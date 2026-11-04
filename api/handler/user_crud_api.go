// This document uses the User Model as an example to demonstrate how to use `crud_service` to
// provide basic CRUD (Create, Read, Update, Delete) api. All you need to do is configure the input,
// output, and search/filter settings. It also supports loading data from related models; simply configure
// the `Relation` in `Preloads` and the `LoadRelation` method in the model.
// Please note that you also need to add an implementation of the `IDModel` interface to the Gorm Model.

package handler

import (
	"gorm.io/gorm"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/db"
)

// UserCreateForm represents user create form data.
type UserCreateForm struct {
	AccountID uint `form:"account_id" json:"account_id" binding:"required"`
}

// ToModel performs the to model operation.
func (u *UserCreateForm) ToModel() *db.User {
	return &db.User{
		AccountID: u.AccountID,
	}
}

// UserUpdateForm represents user update form data.
type UserUpdateForm struct {
	Name      string `form:"name" json:"name" binding:"required"`
	AccountID uint   `form:"account_id" json:"account_id" binding:"required"`
}

// ToModel performs the to model operation.
func (u *UserUpdateForm) ToModel() *db.User {
	p := &db.User{
		AccountID: u.AccountID,
	}
	p.ID = 0
	return p
}

// UserPublicView represents user public view data.
type UserPublicView struct {
	Name      string `json:"name"`
	AccountID uint   `json:"account_id"`
	ID        uint   `json:"id"`
}

// FromModel performs the from model operation.
func (u *UserPublicView) FromModel(p *db.User) *UserPublicView {
	return &UserPublicView{
		ID:        p.ID,
		Name:      "",
		AccountID: p.AccountID,
	}
}

// Preloads performs the preloads operation.
func (u *UserPublicView) Preloads() []string {
	return []string{}
}

// UserSearchInput represents user search input data.
type UserSearchInput struct {
	AccountID uint `form:"account_id"`
}

// GetScopes returns scopes.
func (u *UserSearchInput) GetScopes() []func(*gorm.DB) *gorm.DB {
	var scopes []func(*gorm.DB) *gorm.DB

	// 1. Also, You can add preloads to *gorm.DB at here.

	// 2. Filter by account_id
	if u.AccountID > 0 {
		scopes = append(scopes, func(db *gorm.DB) *gorm.DB {
			return db.Where("account_id", u.AccountID)
		})
	}

	return scopes
}

// UserControl performs the user control operation.
func UserControl(r *base.Router) {
	// Generic parameters：Model, Create, Update, View, Search
	providerService := db.NewCrudService[
		*db.User,
		*UserCreateForm,
		*UserUpdateForm,
		*UserPublicView,
		*UserSearchInput,
	](
		func() *db.User {
			return &db.User{}
		},
		func() *UserPublicView {
			return &UserPublicView{}
		},
		func() *UserSearchInput {
			return &UserSearchInput{}
		},
	)
	providerCtrl := NewCrudController(providerService)
	providerCtrl.RegisterRoutes(r)
}
