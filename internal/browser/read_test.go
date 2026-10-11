package browser

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/chromedp/cdproto"
)

func TestNavigationInterrupted(t *testing.T) {
	for _, message := range []string{"Execution context was destroyed.", "Cannot find context with specified id", "Could not find object with given id", "Inspected target navigated or closed"} {
		e := fmt.Errorf("wrapped: %w", &cdproto.Error{Code: -32000, Message: message})
		if !NavigationInterrupted(e) {
			t.Fatalf("missed navigation error: %v", e)
		}
	}
	for _, e := range []error{nil, context.Canceled, context.DeadlineExceeded, errors.New("Execution context was destroyed."), &cdproto.Error{Code: -32000, Message: "Target closed"}, &cdproto.Error{Code: -32602, Message: "Invalid parameters"}} {
		if NavigationInterrupted(e) {
			t.Fatalf("unrelated failure treated as navigation: %v", e)
		}
	}
}
