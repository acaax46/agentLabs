package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/catalog"
)

func registerCatalog(mux *http.ServeMux, service *app.Service) {
	handle := func(pattern string, fn func(http.ResponseWriter, *http.Request, *catalog.Store)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			store, err := service.Catalog()
			if err != nil {
				catalogError(w, r, err)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			fn(w, r, store)
		})
	}
	handle("GET /api/v1/catalog/summary", func(w http.ResponseWriter, r *http.Request, s *catalog.Store) {
		v, err := s.Summary(r.Context())
		if err != nil {
			catalogError(w, r, err)
			return
		}
		writeJSON(w, 200, v)
	})
	for _, kind := range catalog.Kinds {
		base := "/api/v1/" + kind
		handle("GET "+base, func(w http.ResponseWriter, r *http.Request, s *catalog.Store) {
			params := r.URL.Query()
			q := catalog.Query{Q: params.Get("q"), Status: params.Get("status"), Difficulty: params.Get("difficulty"), ClassID: params.Get("classId")}
			for key, target := range map[string]*int{"page": &q.Page, "pageSize": &q.PageSize} {
				if value := params.Get(key); value != "" {
					n, err := strconv.Atoi(value)
					if err != nil || n < 1 {
						writeError(w, r, 400, "INVALID_QUERY", "分页参数不正确。")
						return
					}
					*target = n
				}
			}
			v, err := s.List(r.Context(), kind, q)
			if err != nil {
				catalogError(w, r, err)
				return
			}
			writeJSON(w, 200, v)
		})
		handle("GET "+base+"/{id}", func(w http.ResponseWriter, r *http.Request, s *catalog.Store) {
			v, err := s.Get(r.Context(), kind, r.PathValue("id"))
			if err != nil {
				catalogError(w, r, err)
				return
			}
			w.Header().Set("ETag", etag(v.Version))
			writeJSON(w, 200, v)
		})
		for method, action := range map[string]string{"POST": "create", "PUT": "update", "DELETE": "delete"} {
			pattern := method + " " + base
			if action != "create" {
				pattern += "/{id}"
			}
			handle(pattern, func(w http.ResponseWriter, r *http.Request, s *catalog.Store) {
				media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if media != "application/json" || r.Header.Get("X-AgentLab-Request") != "catalog" {
					writeError(w, r, 415, "JSON_REQUIRED", "管理操作需要 JSON 请求和产品请求标识。")
					return
				}
				var input catalog.Input
				if action != "delete" {
					r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
					dec := json.NewDecoder(r.Body)
					dec.DisallowUnknownFields()
					if err := dec.Decode(&input); err != nil {
						writeError(w, r, 400, "INVALID_JSON", "请求内容不正确或过大。")
						return
					}
					if err := dec.Decode(new(any)); err != io.EOF {
						writeError(w, r, 400, "INVALID_JSON", "只接受一个 JSON 对象。")
						return
					}
				}
				version := 0
				if action != "create" {
					value := r.Header.Get("If-Match")
					if len(value) < 3 || !strings.HasPrefix(value, "\"") || !strings.HasSuffix(value, "\"") {
						writeError(w, r, 428, "VERSION_REQUIRED", "需要 If-Match 记录版本。")
						return
					}
					version, _ = strconv.Atoi(strings.Trim(value, "\""))
				}
				v, err := s.Mutate(r.Context(), action, kind, r.PathValue("id"), version, r.Header.Get("Idempotency-Key"), input)
				if err != nil {
					catalogError(w, r, err)
					return
				}
				if action == "delete" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Header().Set("ETag", etag(v.Version))
				status := 200
				if action == "create" {
					status = 201
					w.Header().Set("Location", base+"/"+v.ID)
				}
				writeJSON(w, status, v)
			})
		}
	}
}
func etag(version int) string { return "\"" + strconv.Itoa(version) + "\"" }
func catalogError(w http.ResponseWriter, r *http.Request, err error) {
	v := catalog.PublicError(err)
	if v.Status >= 500 {
		slog.Error("catalog operation failed", "request_id", w.Header().Get("X-Request-ID"), "error_type", strings.TrimPrefix(v.Code, "INTERNAL_"))
	}
	writeJSON(w, v.Status, APIError{Code: v.Code, Message: v.Message, RequestID: w.Header().Get("X-Request-ID"), Fields: v.Fields})
}
