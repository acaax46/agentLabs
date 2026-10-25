package records

import (
	"context"
	"errors"
	"testing"

	"agentlab.local/backend/internal/access"
)

func TestPublicErrorPreservesKnownErrors(t *testing.T) {
	denied := access.Denied()
	value := PublicError(denied)
	if value.Code != "FORBIDDEN" || value.Status != 403 || value.Message == "" {
		t.Fatalf("access error not preserved: %+v", value)
	}
	known := &Error{Code: "NOT_FOUND", Message: "missing", Status: 404}
	if got := PublicError(known); got != known {
		t.Fatal("records error not preserved")
	}
	generic := PublicError(errors.New("database failure"))
	if generic.Code != "RECORDS_UNAVAILABLE" || generic.Status != 503 || generic.Message == "" {
		t.Fatalf("generic error leaked: %+v", generic)
	}
	if known.Error() != "missing" {
		t.Fatal("error message missing")
	}
}

func TestRequestIDRoundTrip(t *testing.T) {
	if RequestID(context.Background()) != "" {
		t.Fatal("empty context returned request id")
	}
	ctx := WithRequestID(context.Background(), "request-1")
	if RequestID(ctx) != "request-1" {
		t.Fatal("request id missing")
	}
	ctx = WithRequestID(context.Background(), "")
	if RequestID(ctx) != "" {
		t.Fatal("empty request id accepted")
	}
}

func TestProblemConstructor(t *testing.T) {
	err := problem("CODE", "message", 409)
	var value *Error
	if !errors.As(err, &value) || value.Code != "CODE" || value.Status != 409 || value.Message != "message" {
		t.Fatalf("problem = %#v", err)
	}
}
