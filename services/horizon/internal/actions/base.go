package actions

import (
	"database/sql"
	"net/http"

	"github.com/quantadex/stellar_go/services/horizon/internal/render"
	hProblem "github.com/quantadex/stellar_go/services/horizon/internal/render/problem"
	"github.com/quantadex/stellar_go/services/horizon/internal/render/sse"
	"github.com/quantadex/stellar_go/support/errors"
	"github.com/quantadex/stellar_go/support/log"
	"github.com/quantadex/stellar_go/support/render/problem"
)

// Base is a helper struct you can use as part of a custom action via
// composition.
//
// TODO: example usage
type Base struct {
	W   http.ResponseWriter
	R   *http.Request
	Err error

	isSetup bool
}

// Prepare established the common attributes that get used in nearly every
// action.  "Child" actions may override this method to extend action, but it
// is advised you also call this implementation to maintain behavior.
func (base *Base) Prepare(w http.ResponseWriter, r *http.Request) {
	base.W = w
	base.R = r
}

// Execute trigger content negotiation and the actual execution of one of the
// action's handlers.
func (base *Base) Execute(action interface{}) {
	ctx := base.R.Context()
	contentType := render.Negotiate(base.R)

	switch contentType {
	case render.MimeHal, render.MimeJSON:
		action, ok := action.(JSON)

		if !ok {
			goto NotAcceptable
		}

		action.JSON()

		if base.Err != nil {
			problem.Render(ctx, base.W, base.Err)
			return
		}

	case render.MimeEventStream:
		action, ok := action.(SSE)
		if !ok {
			goto NotAcceptable
		}

		stream := sse.NewStream(ctx, base.W, base.R)

		for {
			action.SSE(stream)

			if base.Err != nil {
				// in the case that we haven't yet sent an event, is also means we
				// havent sent the preamble, meaning we should simply return the normal
				// error.
				if stream.SentCount() == 0 {
					problem.Render(ctx, base.W, base.Err)
					return
				}

				if errors.Cause(base.Err) == sql.ErrNoRows {
					base.Err = errors.New("Object not found")
				} else {
					log.Ctx(ctx).Error(base.Err)
					base.Err = errors.New("Unexpected stream error")
				}

				stream.Err(base.Err)
			}

			if stream.IsDone() {
				return
			}

			select {
			case <-ctx.Done():
				return
			case <-sse.Pumped():
				//no-op, continue onto the next iteration
			}
		}
	case render.MimeRaw:
		action, ok := action.(Raw)

		if !ok {
			goto NotAcceptable
		}

		action.Raw()

		if base.Err != nil {
			problem.Render(ctx, base.W, base.Err)
			return
		}
	default:
		goto NotAcceptable
	}
	return

NotAcceptable:
	problem.Render(ctx, base.W, hProblem.NotAcceptable)
	return
}

// Do executes the provided func iff there is no current error for the action.
// Provides a nicer way to invoke a set of steps that each may set `action.Err`
// during execution
func (base *Base) Do(fns ...func()) {
	for _, fn := range fns {
		if base.Err != nil {
			return
		}

		fn()
	}
}

// Setup runs the provided funcs if and only if no call to Setup() has been
// made previously on this action.
func (base *Base) Setup(fns ...func()) {
	if base.isSetup {
		return
	}
	base.Do(fns...)
	base.isSetup = true
}
