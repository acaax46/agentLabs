package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
)

func TestCatalogSummaryAndIdentityProtection(t *testing.T) {
	h, cat, accounts := managementFixture(t)
	system := access.System(context.Background())
	class, err := cat.Mutate(system, "create", "classes", "", 0, "summary-class", input("SUMMARY-CLASS", "汇总班级", catalog.ClassFields{Teacher: "管理员", Capacity: 30}))
	if err != nil {
		t.Fatal(err)
	}
	student := createAccount(t, accounts, "summarystudent", "student", nil, nil, nil)
	adminToken := loginCookie(t, h, "testowner", "TestOwner-2026!")
	userToken := loginCookie(t, h, student.Username, "TestPassword-2026!")
	for _, entry := range []struct {
		name  string
		token string
	}{
		{"admin", adminToken},
		{"user", userToken},
	} {
		t.Run("summary_"+entry.name, func(t *testing.T) {
			r := call(asCookie(h, entry.token), "GET", "/api/v1/catalog/summary", nil, 0, "")
			requireStatus(t, r, 200)
			var summary catalog.Summary
			if err := json.Unmarshal(r.Body.Bytes(), &summary); err != nil || summary.Counts == nil {
				t.Fatalf("summary = %+v %v", summary, err)
			}
		})
	}
	validStudent := input("SUMMARY-S1", "汇总学员", catalog.StudentFields{ClassIDs: []string{class.ID}})
	requireStatus(t, call(asCookie(h, adminToken), "POST", "/api/v1/students", validStudent, 0, ""), 201)
	missingClass := input("SUMMARY-S2", "无班级学员", catalog.StudentFields{})
	requireStatus(t, call(asCookie(h, adminToken), "POST", "/api/v1/students", missingClass, 0, ""), 201)
	requireStatus(t, call(asCookie(h, userToken), "POST", "/api/v1/students", validStudent, 0, ""), 403)
	r := call(asCookie(h, adminToken), "DELETE", "/api/v1/students/"+student.StudentID, nil, 1, "")
	requireStatus(t, r, 409)
	r = call(asCookie(h, adminToken), "DELETE", "/api/v1/classes/"+class.ID, nil, 1, "")
	requireStatus(t, r, 409)
}
