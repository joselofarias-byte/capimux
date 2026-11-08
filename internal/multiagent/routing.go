package multiagent

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"
)

// Only explicit HTTP rejection permits another model; timeouts are ambiguous.
type upstreamStatusError struct{ status int }

func (e *upstreamStatusError) Error() string { return "upstream request failed" }

func validTask(task string) bool {
	switch task {
	case "", "programming", "review", "analysis", "conversation", "translation", "reasoning":
		return true
	default:
		return false
	}
}

func validStrategy(strategy string) bool {
	switch strategy {
	case "", "parallel", "priority", "fallback", "round-robin", "manual":
		return true
	default:
		return false
	}
}

func dispatch(ctx context.Context, req Request, concurrency int, timeout time.Duration, invoke Invoker, sequence *atomic.Uint64) []Result {
	switch req.Strategy {
	case "manual":
		return Run(ctx, req.Prompt, req.Models[:1], 1, timeout, invoke)
	case "round-robin":
		i := (sequence.Add(1) - 1) % uint64(len(req.Models))
		return Run(ctx, req.Prompt, req.Models[i:i+1], 1, timeout, invoke)
	case "priority", "fallback":
		return runFallback(ctx, req, timeout, invoke)
	default:
		return Run(ctx, req.Prompt, req.Models, concurrency, timeout, invoke)
	}
}

func runFallback(ctx context.Context, req Request, timeout time.Duration, invoke Invoker) []Result {
	results := make([]Result, 0, len(req.Models))
	for _, model := range req.Models {
		var failure error
		capture := func(ctx context.Context, model, prompt string) (string, error) {
			output, err := invoke(ctx, model, prompt)
			failure = err
			return output, err
		}
		result := Run(ctx, req.Prompt, []string{model}, 1, timeout, capture)[0]
		results = append(results, result)
		if ctx.Err() != nil || result.Error == "timed out" || result.Error == "cancelled" || !eligibleFallback(failure) {
			break
		}
	}
	return results
}

func eligibleFallback(err error) bool {
	var status *upstreamStatusError
	if !errors.As(err, &status) {
		return false
	}
	return status.status == http.StatusTooManyRequests || (status.status >= 500 && status.status <= 599)
}
