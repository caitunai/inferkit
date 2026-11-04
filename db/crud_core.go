package db

import (
	"context"

	"gorm.io/gorm"
)

// IDModel The orm model must have id and relations
type IDModel interface {
	GetID() uint
	LoadRelation(ctx context.Context, relations ...string)
}

// InputConverter Transform the input form to orm model (Create/Update)
type InputConverter[T any] interface {
	ToModel() T
}

// ViewConverter Transform orm model to output JSON struct (View)
type ViewConverter[T any, V any] interface {
	FromModel(model T) V
	Preloads() []string
}

// Searcher Must implement to search or filter from database
// it adds gorm scopes when it queries the DB
type Searcher interface {
	GetScopes() []func(*gorm.DB) *gorm.DB
}
