// Package validate checks a struct against its `validate` tags, using go-playground/validator.
// Fields are named by their json tag, so an error reads like the payload a caller sent rather than
// like the Go type behind it.
package validate

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// One validator for the process. It caches the reflection it does per struct type, so sharing it is
// the difference between parsing the tags once and parsing them on every event.
var shared = build()

func build() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	// Without this a failure names the Go field, which is not what the sender wrote.
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			return f.Name
		}
		return name
	})
	return v
}

// Struct reports every rule s breaks, joined into one line — a caller fixing a malformed payload
// wants the whole list, not one field per round trip. s may be a struct or a pointer to one.
//
// Note that an unregistered tag panics inside the library rather than coming back as an error, so a
// typo in a `validate` tag surfaces the first time that type is validated. On the bus that is a
// nacked message via the recovering middleware, not a dead process, but it is still a panic.
func Struct(s any) error {
	err := shared.Struct(s)
	if err == nil {
		return nil
	}
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return err // not a struct at all; the library's own message says so best
	}
	errs := make(fieldErrors, len(ve))
	for i, fe := range ve {
		errs[i] = fmt.Errorf("%s: %s", fe.Field(), reason(fe))
	}
	return errs
}

// reason renders the tags this codebase actually uses. Anything else falls back to naming the tag,
// which is still more use than the library's default sentence.
func reason(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "min":
		return fmt.Sprintf("must be at least %s, got %v", fe.Param(), fe.Value())
	case "max":
		return fmt.Sprintf("must be at most %s, got %v", fe.Param(), fe.Value())
	default:
		if fe.Param() == "" {
			return fmt.Sprintf("fails %q", fe.Tag())
		}
		return fmt.Sprintf("fails %q=%s", fe.Tag(), fe.Param())
	}
}

// fieldErrors reads as one line when logged and is still unwrappable for anyone who wants the
// failures individually. The library's own ValidationErrors prints one per line, which turns a
// single log record into three.
type fieldErrors []error

func (e fieldErrors) Error() string {
	msgs := make([]string, len(e))
	for i, err := range e {
		msgs[i] = err.Error()
	}
	return strings.Join(msgs, "; ")
}

func (e fieldErrors) Unwrap() []error { return e }
