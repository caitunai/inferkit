package base

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Router represents router data.
type Router struct {
	*gin.RouterGroup
	engine *gin.Engine
}

// Controller represents controller data.
type Controller struct {
	r *Router
	h Handler
}

// HandlerFunc represents handler func data.
type HandlerFunc func(c *Context)

// NewRouter creates a new router.
func NewRouter(e *gin.Engine) *Router {
	return &Router{
		RouterGroup: &e.RouterGroup,
		engine:      e,
	}
}

// GET registers an HTTP GET route.
func (r *Router) GET(relativePath string, handlers ...HandlerFunc) *Router {
	r.wrapRoute(http.MethodGet, relativePath, handlers...)
	return r
}

// POST registers an HTTP POST route.
func (r *Router) POST(relativePath string, handlers ...HandlerFunc) *Router {
	r.wrapRoute(http.MethodPost, relativePath, handlers...)
	return r
}

// PUT registers an HTTP PUT route.
func (r *Router) PUT(relativePath string, handlers ...HandlerFunc) *Router {
	r.wrapRoute(http.MethodPut, relativePath, handlers...)
	return r
}

// PATCH registers an HTTP PATCH route.
func (r *Router) PATCH(relativePath string, handlers ...HandlerFunc) *Router {
	r.wrapRoute(http.MethodPatch, relativePath, handlers...)
	return r
}

// HEAD registers an HTTP HEAD route.
func (r *Router) HEAD(relativePath string, handlers ...HandlerFunc) *Router {
	r.wrapRoute(http.MethodHead, relativePath, handlers...)
	return r
}

// OPTIONS registers an HTTP OPTIONS route.
func (r *Router) OPTIONS(relativePath string, handlers ...HandlerFunc) *Router {
	r.wrapRoute(http.MethodOptions, relativePath, handlers...)
	return r
}

// DELETE registers an HTTP DELETE route.
func (r *Router) DELETE(relativePath string, handlers ...HandlerFunc) *Router {
	r.wrapRoute(http.MethodDelete, relativePath, handlers...)
	return r
}

// CONNECT registers an HTTP CONNECT route.
func (r *Router) CONNECT(relativePath string, handlers ...HandlerFunc) *Router {
	r.wrapRoute(http.MethodConnect, relativePath, handlers...)
	return r
}

// TRACE registers an HTTP TRACE route.
func (r *Router) TRACE(relativePath string, handlers ...HandlerFunc) *Router {
	r.wrapRoute(http.MethodTrace, relativePath, handlers...)
	return r
}

// Use performs the use operation.
func (r *Router) Use(handlers ...HandlerFunc) *Router {
	r.wrapRoute("use", "", handlers...)
	return r
}

// NoRoute performs the no route operation.
func (r *Router) NoRoute(handlers ...HandlerFunc) *Router {
	hds := make([]gin.HandlerFunc, 0, len(handlers))
	for _, hd := range handlers {
		hds = append(hds, wrapHandler(hd))
	}
	if r.engine != nil {
		r.engine.NoRoute(hds...)
	}
	return r
}

// Group performs the group operation.
func (r *Router) Group(relativePath string, handlers ...HandlerFunc) *Router {
	g := r.RouterGroup.Group(relativePath, wrapHandlers(handlers)...)
	return &Router{
		RouterGroup: g,
		engine:      r.engine,
	}
}

func (r *Router) wrapRoute(method, relativePath string, handlers ...HandlerFunc) {
	hds := wrapHandlers(handlers)
	if method == "use" {
		r.RouterGroup.Use(hds...)
		return
	}
	r.Handle(method, relativePath, hds...)
}

func wrapHandler(hd HandlerFunc) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hd(&Context{Context: ctx})
	}
}

// Controller performs the controller operation.
func (r *Router) Controller(h Handler) *Controller {
	return &Controller{r: r, h: h}
}

// GET registers an HTTP GET route.
func (c *Controller) GET(relativePath, action string) *Controller {
	c.r.GET(relativePath, c.cloneHandler(action, c.h))
	return c
}

// POST registers an HTTP POST route.
func (c *Controller) POST(relativePath, action string) *Controller {
	c.r.POST(relativePath, c.cloneHandler(action, c.h))
	return c
}

// PUT registers an HTTP PUT route.
func (c *Controller) PUT(relativePath, action string) *Controller {
	c.r.PUT(relativePath, c.cloneHandler(action, c.h))
	return c
}

// PATCH registers an HTTP PATCH route.
func (c *Controller) PATCH(relativePath, action string) *Controller {
	c.r.PATCH(relativePath, c.cloneHandler(action, c.h))
	return c
}

// HEAD registers an HTTP HEAD route.
func (c *Controller) HEAD(relativePath, action string) *Controller {
	c.r.HEAD(relativePath, c.cloneHandler(action, c.h))
	return c
}

// OPTIONS registers an HTTP OPTIONS route.
func (c *Controller) OPTIONS(relativePath, action string) *Controller {
	c.r.OPTIONS(relativePath, c.cloneHandler(action, c.h))
	return c
}

// DELETE registers an HTTP DELETE route.
func (c *Controller) DELETE(relativePath, action string) *Controller {
	c.r.DELETE(relativePath, c.cloneHandler(action, c.h))
	return c
}

// CONNECT registers an HTTP CONNECT route.
func (c *Controller) CONNECT(relativePath, action string) *Controller {
	c.r.CONNECT(relativePath, c.cloneHandler(action, c.h))
	return c
}

// TRACE registers an HTTP TRACE route.
func (c *Controller) TRACE(relativePath, action string) *Controller {
	c.r.TRACE(relativePath, c.cloneHandler(action, c.h))
	return c
}

// Use performs the use operation.
func (c *Controller) Use(action string) *Controller {
	c.r.wrapRoute("use", "", c.cloneHandler(action, c.h))
	return c
}

// Group performs the group operation.
func (c *Controller) Group(relativePath, action string) *Controller {
	g := c.r.Group(relativePath, c.cloneHandler(action, c.h)).RouterGroup
	r := &Router{
		RouterGroup: g,
	}
	return r.Controller(c.h)
}

func wrapHandlers(handlers []HandlerFunc) []gin.HandlerFunc {
	hds := make([]gin.HandlerFunc, 0, len(handlers))
	for _, handler := range handlers {
		hds = append(hds, wrapHandler(handler))
	}
	return hds
}

// Resource performs the resource operation.
func (c *Controller) Resource(relativePath string) *Controller {
	c.GET(relativePath, http.MethodGet)
	c.POST(relativePath, http.MethodPost)
	c.PUT(relativePath, http.MethodPut)
	c.PATCH(relativePath, http.MethodPatch)
	c.DELETE(relativePath, http.MethodDelete)
	return c
}

func (c *Controller) cloneHandler(action string, h Handler) HandlerFunc {
	return func(c *Context) {
		hd := h.Clone().GetHandler(action)
		if hd != nil {
			hd(c)
		} else {
			c.Error(http.StatusInternalServerError, action+" handler not implemented", gin.H{})
		}
	}
}
