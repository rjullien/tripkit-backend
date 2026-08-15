package bifrost

// options holds per-call overrides resolved from functional options.
type options struct {
	MaxTokens int
	Model     string
}

func defaults() options {
	return options{MaxTokens: 800}
}

// Option configures a single call.
type Option func(*options)

// WithMaxTokens overrides the default max_tokens for a request.
func WithMaxTokens(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.MaxTokens = n
		}
	}
}

// WithModel overrides the client-level model for a single request.
func WithModel(m string) Option {
	return func(o *options) {
		if m != "" {
			o.Model = m
		}
	}
}
