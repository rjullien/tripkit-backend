package bifrost

import "context"

// Completer is the LLM seam (real Client in prod, fake/CompleteFn in tests).
type Completer interface {
	Complete(system, user string) (string, error)
}

// CompleteFn adapts a plain function to the Completer interface (test helper).
type CompleteFn func(system, user string) (string, error)

// Complete calls the underlying function.
func (f CompleteFn) Complete(system, user string) (string, error) {
	return f(system, user)
}

// completerAdapter wraps a Client so it satisfies Completer with default model.
type completerAdapter struct {
	client *Client
}

// Complete satisfies the Completer interface using the client-level model.
func (a *completerAdapter) Complete(system, user string) (string, error) {
	return a.client.Complete(context.Background(), "", system, user)
}

// AsCompleter returns a Completer that uses the client's default model.
func (c *Client) AsCompleter() Completer {
	return &completerAdapter{client: c}
}
