package platform

import (
	"errors"
	"net/http"

	"agentlab.local/backend/internal/access"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Errors are *access.Error values so the existing public-error plumbing keeps working. Codes are
// stable API contract; messages are for people and may change.
func problem(code, message string, status int) error {
	return &access.Error{Code: code, Message: message, Status: status}
}

func invalid(message string) error {
	return problem("VALIDATION_ERROR", message, http.StatusUnprocessableEntity)
}

func invalidFields(message string, fields map[string]string) error {
	return &access.Error{Code: "VALIDATION_ERROR", Message: message, Fields: fields, Status: http.StatusUnprocessableEntity}
}

// notFound deliberately covers "missing" and "not yours" alike so IDs cannot be probed.
func notFound() error {
	return problem("NOT_FOUND", "资源不存在或无权访问。", http.StatusNotFound)
}

// invalidCursor is its own code so a client can tell "start the listing again" from a bad request.
func invalidCursor() error {
	return problem("INVALID_CURSOR", "分页游标无效，请从第一页重新开始。", http.StatusBadRequest)
}

func versionConflict() error {
	return problem("VERSION_CONFLICT", "资料已变化，请刷新后重试。", http.StatusConflict)
}

func unavailable(code, message string) error {
	return problem(code, message, http.StatusServiceUnavailable)
}

// isDuplicate reports a unique-index violation, including inside a transaction.
func isDuplicate(err error) bool { return mongo.IsDuplicateKeyError(err) }

// asProblem returns the *access.Error inside err, if any.
func asProblem(err error) (*access.Error, bool) {
	var known *access.Error
	if errors.As(err, &known) {
		return known, true
	}
	return nil, false
}
