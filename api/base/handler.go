package base

// Handler represents handler data.
type Handler interface {
	GetHandler(action string) HandlerFunc
	Clone() Handler
}

// BasicHandler represents basic handler data.
type BasicHandler struct {
	Handlers map[string]HandlerFunc
}

// SetHandler sets handler.
func (b *BasicHandler) SetHandler(action string, handlerFunc HandlerFunc) {
	if b.Handlers == nil {
		b.Handlers = make(map[string]HandlerFunc)
	}
	b.Handlers[action] = handlerFunc
}

// GetHandler returns handler.
func (b *BasicHandler) GetHandler(action string) HandlerFunc {
	return b.Handlers[action]
}
