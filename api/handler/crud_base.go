package handler

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/db"
)

const (
	// KeyModel identifies the "model" value.
	KeyModel = "model"
	// KeyError identifies the "error" value.
	KeyError = "error"
)

// ErrInvalidID indicates invalid id.
var ErrInvalidID = errors.New("invalid id")

// IDQueryAll represents id query all data.
type IDQueryAll struct {
	IDs []uint `form:"id[]" json:"ids" binding:"required"`
}

// CrudController represents crud controller data.
type CrudController[M db.IDModel, C db.InputConverter[M], U db.InputConverter[M], V db.ViewConverter[M, V], S db.Searcher] struct {
	Service *db.CrudService[M, C, U, V, S]
}

// NewCrudController creates a new crud controller.
func NewCrudController[M db.IDModel, C, U db.InputConverter[M], V db.ViewConverter[M, V], S db.Searcher](s *db.CrudService[M, C, U, V, S]) *CrudController[M, C, U, V, S] {
	return &CrudController[M, C, U, V, S]{Service: s}
}

// RegisterRoutes performs the register routes operation.
func (ctrl *CrudController[M, C, U, V, S]) RegisterRoutes(r *base.Router) {
	r.POST("", ctrl.Create)
	r.GET("/:id", ctrl.Get)
	r.GET("/list", ctrl.GetAll)
	r.PUT("/:id", ctrl.Update)
	r.DELETE("/:id", ctrl.Delete)
	r.GET("", ctrl.List)
}

// Create data to database
func (ctrl *CrudController[M, C, U, V, S]) Create(c *base.Context) {
	var input C
	if err := c.ShouldBindJSON(&input); err != nil {
		c.ErrorForm(err.Error(), gin.H{})
		return
	}
	view, err := ctrl.Service.Create(c, input)
	if err != nil {
		c.ErrorMessage(err.Error())
		return
	}
	c.Success(gin.H{
		KeyModel: view,
	})
}

// Get one item details, with path param: /:id
func (ctrl *CrudController[M, C, U, V, S]) Get(c *base.Context) {
	id, err := positiveID(c.Param("id"))
	if err != nil {
		c.ErrorForm(ErrInvalidID.Error(), gin.H{})
		return
	}
	view, err := ctrl.Service.Get(c, uint(id))
	if err != nil {
		c.NotFound(err.Error(), gin.H{KeyError: "not found"})
		return
	}
	c.Success(gin.H{
		KeyModel: view,
	})
}

// GetAll Get details of items in array, with query params: ?ids[]=1&ids[]=2
func (ctrl *CrudController[M, C, U, V, S]) GetAll(c *base.Context) {
	q := &IDQueryAll{}
	if err := c.ShouldBind(q); err != nil {
		c.ErrorForm(err.Error(), gin.H{})
		return
	}
	view, err := ctrl.Service.GetAll(c, q.IDs)
	if err != nil {
		c.NotFound(err.Error(), gin.H{KeyError: "not found"})
		return
	}
	c.Success(gin.H{
		"models": view,
	})
}

// Update item details, with path param: /:id
func (ctrl *CrudController[M, C, U, V, S]) Update(c *base.Context) {
	id, err := positiveID(c.Param("id"))
	if err != nil {
		c.ErrorForm(ErrInvalidID.Error(), gin.H{})
		return
	}
	var input U
	if bindErr := c.ShouldBindJSON(&input); bindErr != nil {
		c.ErrorForm(bindErr.Error(), gin.H{})
		return
	}
	view, err := ctrl.Service.Update(c, uint(id), input)
	if err != nil {
		c.ErrorMessage(err.Error())
		return
	}
	c.Success(gin.H{
		KeyModel: view,
	})
}

// Delete one item with path param: /:id
func (ctrl *CrudController[M, C, U, V, S]) Delete(c *base.Context) {
	id, err := positiveID(c.Param("id"))
	if err != nil {
		c.ErrorForm(ErrInvalidID.Error(), gin.H{})
		return
	}

	if deleteErr := ctrl.Service.Delete(uint(id)); deleteErr != nil {
		// Return a stable not-found response while preserving other service errors.
		if errors.Is(deleteErr, db.ErrRecordNotFound) {
			c.NotFound(db.ErrRecordNotFound.Error(), gin.H{})
			return
		}
		c.ErrorMessage(deleteErr.Error())
		return
	}

	c.Success(gin.H{
		"id": id,
	})
}

func positiveID(value string) (int, error) {
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return 0, errors.Join(ErrInvalidID, err)
	}
	return id, nil
}

// List : Automatically bind pagination and search parameters
func (ctrl *CrudController[M, C, U, V, S]) List(c *base.Context) {
	// 1. Bind pagination parameters
	var pageReq db.PagingRequest
	if err := c.ShouldBindQuery(&pageReq); err != nil {
		c.ErrorForm("invalid paging params", gin.H{})
		return
	}

	// 2. Bind search parameters (Generic S)
	searchPtr := ctrl.Service.NewSearcher()
	if err := c.ShouldBindQuery(searchPtr); err != nil {
		c.ErrorForm("invalid search params", gin.H{})
		return
	}

	// 3. Call service
	res, err := ctrl.Service.ListCursor(pageReq, searchPtr)
	if err != nil {
		c.ErrorMessage(err.Error())
		return
	}
	c.Success(gin.H{
		"pagination": res,
	})
}
