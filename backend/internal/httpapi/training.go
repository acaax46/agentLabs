package httpapi

import (
	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/training"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"time"
)

func registerTraining(mux *http.ServeMux, service *app.Service) {
	withStore := func(fn func(http.ResponseWriter, *http.Request, *training.Store)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			store, err := service.Training()
			if err != nil {
				catalogError(w, r, err)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			fn(w, r.WithContext(ctx), store)
		}
	}
	mux.HandleFunc("GET /api/v1/training/problems", withStore(func(w http.ResponseWriter, r *http.Request, store *training.Store) {
		items, err := store.ListProblems(r.Context(), training.Query{Q: r.URL.Query().Get("q"), Difficulty: r.URL.Query().Get("difficulty")})
		if err != nil {
			catalogError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items})
	}))
	mux.HandleFunc("GET /api/v1/training/problems/{id}", withStore(func(w http.ResponseWriter, r *http.Request, store *training.Store) {
		value, err := store.GetProblem(r.Context(), r.PathValue("id"))
		if err != nil {
			catalogError(w, r, err)
			return
		}
		writeJSON(w, 200, value)
	}))
	mux.HandleFunc("POST /api/v1/training/problems/{id}/workspace", withStore(func(w http.ResponseWriter, r *http.Request, store *training.Store) {
		if r.Header.Get("X-AgentLab-Request") != "training" {
			writeError(w, r, 415, "REQUEST_MARKER_REQUIRED", "训练操作缺少产品请求标识。")
			return
		}
		value, err := store.OpenProblem(r.Context(), r.PathValue("id"))
		if err != nil {
			catalogError(w, r, err)
			return
		}
		writeJSON(w, 200, value)
	}))
	mux.HandleFunc("GET /api/v1/training/courses", withStore(func(w http.ResponseWriter, r *http.Request, store *training.Store) {
		items, err := store.ListCourses(r.Context())
		if err != nil {
			catalogError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items})
	}))
	mux.HandleFunc("GET /api/v1/training/courses/{id}", withStore(func(w http.ResponseWriter, r *http.Request, store *training.Store) {
		value, err := store.GetCourse(r.Context(), r.PathValue("id"))
		if err != nil {
			catalogError(w, r, err)
			return
		}
		writeJSON(w, 200, value)
	}))
	mux.HandleFunc("PUT /api/v1/training/courses/{id}/progress", withStore(func(w http.ResponseWriter, r *http.Request, store *training.Store) {
		media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if media != "application/json" || r.Header.Get("X-AgentLab-Request") != "training" {
			writeError(w, r, 415, "JSON_REQUIRED", "学习进度需要 JSON 请求和产品请求标识。")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var input training.ProgressInput
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeError(w, r, 400, "INVALID_JSON", "学习进度内容不正确。")
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			writeError(w, r, 400, "INVALID_JSON", "只允许一个 JSON 对象。")
			return
		}
		value, err := store.SaveProgress(r.Context(), r.PathValue("id"), input)
		if err != nil {
			catalogError(w, r, err)
			return
		}
		writeJSON(w, 200, value)
	}))
}
