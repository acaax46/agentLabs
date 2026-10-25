package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/records"
)

func registerRecords(mux *http.ServeMux, service *app.Service) {
	for _, kind := range records.Kinds {
		base := "/api/v1/" + kind
		read := func(detail bool) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				store, err := service.Records()
				if err != nil {
					recordError(w, r, err)
					return
				}
				ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
				defer cancel()
				if detail {
					value, err := store.Get(ctx, kind, r.PathValue("id"))
					if err != nil {
						recordError(w, r, err)
						return
					}
					writeJSON(w, 200, value)
					return
				}
				p := r.URL.Query()
				q := records.Query{Q: p.Get("q"), Status: p.Get("status"), Verdict: p.Get("verdict"), StudentID: p.Get("studentId"), ProblemID: p.Get("problemId"), AssignmentID: p.Get("assignmentId"), ContestID: p.Get("contestId"), Provider: p.Get("provider"), Model: p.Get("model"), Source: p.Get("source"), Settlement: p.Get("settlement"), OperationID: p.Get("operationId"), RequestID: p.Get("requestId"), Category: p.Get("category"), Level: p.Get("level"), Result: p.Get("result"), ResourceType: p.Get("resourceType"), ResourceID: p.Get("resourceId"), From: p.Get("from"), To: p.Get("to")}
				for key, target := range map[string]*int{"page": &q.Page, "pageSize": &q.PageSize} {
					if value := p.Get(key); value != "" {
						n, err := strconv.Atoi(value)
						if err != nil || n < 1 {
							writeError(w, r, 400, "INVALID_QUERY", "分页参数不正确。")
							return
						}
						*target = n
					}
				}
				value, err := store.List(ctx, kind, q)
				if err != nil {
					recordError(w, r, err)
					return
				}
				writeJSON(w, 200, value)
			}
		}
		mux.HandleFunc("GET "+base, read(false))
		mux.HandleFunc("GET "+base+"/{id}", read(true))
		deny := func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, r, 405, "APPEND_ONLY_RECORDS", "运行记录由后端追加，不支持手工创建、修改或删除。")
		}
		mux.HandleFunc(base, deny)
		mux.HandleFunc(base+"/{id}", deny)
	}
}
func recordError(w http.ResponseWriter, r *http.Request, err error) {
	value := records.PublicError(err)
	writeError(w, r, value.Status, value.Code, value.Message)
}
