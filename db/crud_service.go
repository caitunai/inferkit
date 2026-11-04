package db

import (
	"context"
	"errors"
	"slices"

	"gorm.io/gorm"
)

// ErrRecordNotFound indicates that a CRUD target does not exist.
var ErrRecordNotFound = errors.New("record not found")

// CrudService represents crud service data.
type CrudService[M IDModel, C InputConverter[M], U InputConverter[M], V ViewConverter[M, V], S Searcher] struct {
	DB              *gorm.DB
	NewModel        func() M
	NewResponseView func() V
	NewSearcher     func() S
}

// NewCrudService creates a new crud service.
func NewCrudService[M IDModel, C, U InputConverter[M], V ViewConverter[M, V], S Searcher](
	model func() M,
	responseView func() V,
	searcher func() S,
) *CrudService[M, C, U, V, S] {
	return &CrudService[M, C, U, V, S]{
		DB:              db,
		NewModel:        model,
		NewResponseView: responseView,
		NewSearcher:     searcher,
	}
}

// Create /Update/Get should not change...
func (s *CrudService[M, C, U, V, S]) Create(ctx context.Context, input C) (V, error) {
	var view V
	model := input.ToModel()
	if err := s.DB.WithContext(ctx).Create(model).Error; err != nil {
		return view, err
	}
	view = s.NewResponseView()
	for _, r := range view.Preloads() {
		model.LoadRelation(ctx, r)
	}
	return view.FromModel(model), nil
}

// Update performs the update operation.
func (s *CrudService[M, C, U, V, S]) Update(ctx context.Context, id uint, input U) (V, error) {
	var model M
	var view V
	model = s.NewModel()
	if err := s.DB.WithContext(ctx).First(model, id).Error; err != nil {
		return view, err
	}
	updates := input.ToModel()
	if err := s.DB.WithContext(ctx).Model(model).Updates(updates).Error; err != nil {
		return view, err
	}
	s.DB.WithContext(ctx).First(model, id)
	view = s.NewResponseView()
	for _, r := range view.Preloads() {
		model.LoadRelation(ctx, r)
	}
	return view.FromModel(model), nil
}

// Get performs the get operation.
func (s *CrudService[M, C, U, V, S]) Get(ctx context.Context, id uint) (V, error) {
	var model M
	var view V
	model = s.NewModel()
	if err := s.DB.WithContext(ctx).First(model, id).Error; err != nil {
		return view, err
	}
	view = s.NewResponseView()
	for _, r := range view.Preloads() {
		model.LoadRelation(ctx, r)
	}
	return view.FromModel(model), nil
}

// GetAll returns all.
func (s *CrudService[M, C, U, V, S]) GetAll(ctx context.Context, ids []uint) ([]V, error) {
	var models []M
	var view V
	q := s.DB.WithContext(ctx).Where("id IN ?", ids)
	for _, r := range view.Preloads() {
		q.Preload(r)
	}
	if err := q.Find(&models).Error; err != nil {
		return make([]V, 0), err
	}
	views := make([]V, 0, len(models))
	view = s.NewResponseView()
	for _, m := range models {
		views = append(views, view.FromModel(m))
	}
	return views, nil
}

// Delete data by id
func (s *CrudService[M, C, U, V, S]) Delete(id uint) error {
	model := s.NewModel()
	// 1. check it is existed
	if err := s.DB.First(model, id).Error; err != nil {
		return classifyRecordNotFound(err)
	}

	// 2. do delete
	// If the model include gorm.DeletedAt，GORM will do softly delete,
	// or it will delete the data permanently
	return s.DB.Delete(model).Error
}

func classifyRecordNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.Join(ErrRecordNotFound, err)
	}
	return err
}

// ListCursor ================== Query or filter data list ==================
//
//nolint:cyclop,funlen,gocognit // This bounded database lifecycle keeps pool ownership and query classification explicit.
func (s *CrudService[M, C, U, V, S]) ListCursor(req PagingRequest, search S) (*PagingResponse[V], error) {
	var models []M
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	// safe: max limit
	if limit > 100 {
		limit = 100
	}

	queryLimit := limit + 1 // get 1 more data

	model := s.NewModel()
	tx := s.DB.Model(model)

	// 1. add Scopes from user defined
	scopes := search.GetScopes()
	for _, scope := range scopes {
		tx = tx.Scopes(scope)
	}

	// 2. confirm the direction of query
	// isNextDirection: true, need old data (ID become much smaller), mean next page
	isNextDirection := !req.Prev

	if isNextDirection {
		// Go to next page (Next): ID < Cursor, ORDER ID DESC
		if req.Cursor > 0 {
			tx = tx.Where("id < ?", req.Cursor)
		}
		tx = tx.Order("id DESC")
	} else {
		// Go to prev page (Prev): ID > Cursor, ORDER ID ASC (To retrieve the data immediately following the cursor)
		if req.Cursor > 0 {
			tx = tx.Where("id > ?", req.Cursor)
		}
		tx = tx.Order("id ASC")
	}

	// 3. Do DB query
	if err := tx.Limit(queryLimit).Find(&models).Error; err != nil {
		return nil, err
	}

	// 4. Determine whether an extra entry was found (the “more” flag that determines the current direction)
	hasMoreInCurrentDirection := false
	if len(models) > limit {
		hasMoreInCurrentDirection = true
		models = models[:limit] // Remove the extra record.
	}

	// 5. Data Reversal
	// When scrolling up (Prev), we retrieve data in ASC order (e.g., 101, 102, 103)
	// However, the front end typically displays data in reverse chronological order (103, 102, 101), so we need to reverse it
	if !isNextDirection {
		slices.Reverse(models)
	}

	// 6. Core: Calculating HasNext and HasPrev
	var hasNext, hasPrev bool

	if isNextDirection {
		// Scenario: Clicking “Next Page”
		hasNext = hasMoreInCurrentDirection // I just found another one, which means there are even older ones
		hasPrev = req.Cursor > 0            // As long as there is a cursor, it means we are not on the first page, so there must be a previous page
	} else {
		// Scenario: Clicking “Previous”
		hasPrev = hasMoreInCurrentDirection // I just found another one, which means there are even newer ones
		hasNext = true                      // Since we're searching backward, that means there must be data at the original position
	}

	// Boundary correction: If the result set is empty, reset the flag
	if len(models) == 0 {
		hasNext = false
		hasPrev = false
	}

	// 7. Convert Data View
	vInstance := s.NewResponseView()
	views := make([]V, 0, len(models))
	for _, m := range models {
		views = append(views, vInstance.FromModel(m))
	}

	// 8. Calculate the start and end cursors
	var nextCursor, prevCursor uint
	if len(models) > 0 {
		prevCursor = models[0].GetID()
		nextCursor = models[len(models)-1].GetID()
	}

	return &PagingResponse[V]{
		Data:       views,
		NextCursor: nextCursor,
		PrevCursor: prevCursor,
		HasNext:    hasNext,
		HasPrev:    hasPrev,
	}, nil
}
