package compound

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/samibel/graphi/engine/query"
)

// Args is the operation's wire argument shape. Query is passed through
// verbatim: Parse owns validation and defaulting, exactly as on the legacy
// Direct.Compound path.
type Args struct {
	Query string `json:"query"`
}

// Handler returns the compound operation handler bound to the narrow read-only
// query port it needs. It parses, executes and serializes through the same
// engine functions as the legacy path; surfaces never see its params type.
func Handler(reader query.Reader) func(context.Context, json.RawMessage) ([]byte, error) {
	return func(ctx context.Context, raw json.RawMessage) ([]byte, error) {
		args, err := decodeArgs(raw)
		if err != nil {
			return nil, err
		}
		parsed, err := Parse(args.Query)
		if err != nil {
			return nil, err
		}
		result, err := Execute(ctx, reader, parsed)
		if err != nil {
			return nil, err
		}
		return query.Marshal(result)
	}
}

func decodeArgs(raw json.RawMessage) (Args, error) {
	var args Args
	if len(bytes.TrimSpace(raw)) == 0 {
		return args, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return Args{}, fmt.Errorf("%s: decode arguments: %w", Operation, err)
	}
	return args, nil
}
