package interceptor

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
)

type policyFunc func(context.Context, Request) (Decision, error)

func (f policyFunc) Check(c context.Context, r Request) (Decision, error) { return f(c, r) }
func TestEvaluation(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, Evaluate(ctx, AllowAll{}, Request{Command: "anything"}))
	for _, p := range []Interceptor{nil, policyFunc(func(context.Context, Request) (Decision, error) {
		return Decision{}, errors.New("secret backend error")
	}), policyFunc(func(context.Context, Request) (Decision, error) { return Decision{}, nil })} {
		err := Evaluate(ctx, p, Request{})
		var rejection *Rejection
		require.ErrorAs(t, err, &rejection)
		require.NotContains(t, err.Error(), "secret")
	}
}
