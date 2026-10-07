package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"my-bookmark/internal/store"
	"my-bookmark/internal/summary"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func summaryData(a store.Article) map[string]any {
	status := a.Status
	if status == "processing" && !a.LeaseUntil.After(time.Now()) {
		status = "interrupted"
	}
	retryable := status == "ready" || status == "pending" || status == "failed" || status == "interrupted"
	return map[string]any{"status": status, "stage": a.Stage, "progress": a.Progress, "error": a.LastError, "sources": a.Sources, "points": a.Points, "tldr": a.TLDR, "title": a.Title, "retryable": retryable}
}
func (a *App) summaryStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := a.id(w, r)
	if !ok {
		return
	}
	article, err := a.db.Get(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, summaryData(article))
}
func (a *App) generate(w http.ResponseWriter, r *http.Request) {
	id, ok := a.id(w, r)
	if !ok {
		return
	}
	style := summary.Style(r.PostFormValue("style"))
	if !style.Valid() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "要約の種類を選び直してください。"})
		return
	}
	state, err := a.db.Enqueue(r.Context(), session(r).User.ID, id, style)
	if errors.Is(err, store.ErrRateLimited) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "要約の利用上限です（1分3件・1日50件）。時間をおいて再試行してください。"})
		return
	}
	if errors.Is(err, store.ErrBusy) {
		article, e := a.db.Get(r.Context(), id)
		if e != nil {
			a.fail(w, r, e)
			return
		}
		writeJSON(w, http.StatusAccepted, summaryData(article))
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		a.problem(w, r, 404, "ブックマークが見つかりません。")
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	article, err := a.db.Get(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	result := summaryData(article)
	result["status"] = state
	writeJSON(w, http.StatusAccepted, result)
}
