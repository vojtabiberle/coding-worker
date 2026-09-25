package runner

import (
	"context"
	"fmt"
)

func For(engine string) Engine {
	switch engine {
	case "opencode":
		return OpenCode{}
	case "pi":
		return Pi{}
	default:
		return unsupportedEngine(engine)
	}
}

type unsupportedEngine string

func (e unsupportedEngine) Start(context.Context, Request) (Result, error) {
	return Result{}, fmt.Errorf("unsupported engine %q", e)
}
func (e unsupportedEngine) Continue(context.Context, string, Request) (Result, error) {
	return Result{}, fmt.Errorf("unsupported engine %q", e)
}
func Version(ctx context.Context, engine string) (string, error) {
	switch engine {
	case "opencode":
		return (OpenCode{}).Version(ctx)
	case "pi":
		return (Pi{}).Version(ctx)
	default:
		return "", fmt.Errorf("unsupported engine %q", engine)
	}
}
