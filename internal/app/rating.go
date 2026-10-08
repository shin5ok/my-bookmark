package app

import (
	"net/http"
	"strconv"
)

func (a *App) setRating(w http.ResponseWriter, r *http.Request) {
	id, ok := a.id(w, r)
	if !ok {
		return
	}
	rating, err := strconv.Atoi(r.PostFormValue("rating"))
	if err != nil || rating < 0 || rating > 5 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "星は1〜5個で評価してください。"})
		return
	}
	total, err := a.db.SetRating(r.Context(), session(r).User.ID, id, rating)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rating": rating, "rating_total": total})
}
