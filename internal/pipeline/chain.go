package pipeline

type Handler interface {
	Handle(ctx *Context) error
}

type HandlerFunc func(ctx *Context) error

func (f HandlerFunc) Handle(ctx *Context) error {
	return f(ctx)
}

type Chain struct {
	handlers []Handler
}

func NewChain(handlers ...Handler) *Chain {
	chain := &Chain{
		handlers: make([]Handler, 0, len(handlers)),
	}
	for _, handler := range handlers {
		if handler != nil {
			chain.handlers = append(chain.handlers, handler)
		}
	}

	return chain
}

func (c *Chain) Run(ctx *Context) error {
	if c == nil {
		return nil
	}

	for _, handler := range c.handlers {
		if err := handler.Handle(ctx); err != nil {
			return err
		}
	}

	return nil
}
