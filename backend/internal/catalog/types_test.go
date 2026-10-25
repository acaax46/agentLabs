package catalog

import (
	"errors"
	"testing"

	"agentlab.local/backend/internal/access"
)

func TestCheckKindAndPublicError(t *testing.T) {
	for _, kind := range Kinds {
		if err := CheckKind(kind); err != nil {
			t.Fatalf("known kind %q: %v", kind, err)
		}
	}
	err := CheckKind("unknown")
	var known *Error
	if !errors.As(err, &known) || known.Code != "NOT_FOUND" || known.Status != 404 {
		t.Fatalf("unknown kind = %#v", err)
	}
	denied := PublicError(access.Denied())
	if denied.Code != "FORBIDDEN" || denied.Status != 403 || denied.Fields != nil {
		t.Fatalf("access error not preserved: %+v", denied)
	}
	validation := &access.Error{Code: "VALIDATION_ERROR", Message: "check", Fields: map[string]string{"code": "bad"}, Status: 422}
	preserved := PublicError(validation)
	if preserved.Code != validation.Code || preserved.Fields["code"] != "bad" || preserved.Status != 422 {
		t.Fatalf("validation fields not preserved: %+v", preserved)
	}
	own := &Error{Code: "OWN", Message: "own", Status: 409}
	if PublicError(own) != own || own.Error() != "own" {
		t.Fatal("catalog error not preserved")
	}
	generic := PublicError(errors.New("unexpected"))
	if generic.Code != "INTERNAL_ERROR" || generic.Status != 500 || generic.Message == "" {
		t.Fatalf("generic error leaked: %+v", generic)
	}
}
